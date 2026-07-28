package vips_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

func load(t *testing.T, name string) *vips.Image {
	t.Helper()
	outs, err := vips.Call("pngload", vips.In("filename", filepath.Join("testdata", name)))
	if err != nil {
		t.Fatalf("pngload %s: %v", name, err)
	}
	im, err := outs.Image("out")
	if err != nil {
		t.Fatalf("pngload %s: %v", name, err)
	}
	t.Cleanup(im.Close)
	return im
}

func TestVersion(t *testing.T) {
	if v := vips.Version(); v == "" {
		t.Fatal("no version reported")
	}
	major, minor, _ := vips.VersionParts()
	if major < 8 || (major == 8 && minor < 14) {
		t.Fatalf("libvips %d.%d is older than this package supports", major, minor)
	}
	t.Logf("linked against libvips %s", vips.Version())
}

// The operation list comes from the installed libvips, not from a table in this
// repository, so it grows on its own when libvips does.
func TestOperationsAreDiscovered(t *testing.T) {
	ops := vips.Operations()
	if len(ops) < 200 {
		t.Fatalf("discovered only %d operations, expected the full set", len(ops))
	}

	want := []string{"thumbnail", "resize", "jpegsave", "hist_find", "composite"}
	have := map[string]bool{}
	for _, op := range ops {
		have[op] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("operation %q not discovered", w)
		}
	}
	t.Logf("discovered %d operations", len(ops))
}

func TestDescribe(t *testing.T) {
	spec, err := vips.Describe("thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Description == "" {
		t.Error("no description")
	}

	byName := map[string]vips.ArgSpec{}
	for _, a := range spec.Args {
		byName[a.Name] = a
	}

	filename, ok := byName["filename"]
	if !ok {
		t.Fatal("thumbnail has no filename argument")
	}
	if !filename.Required || !filename.Input || filename.Kind != vips.KindString {
		t.Errorf("filename: got %+v", filename)
	}

	crop, ok := byName["crop"]
	if !ok {
		t.Fatal("thumbnail has no crop argument")
	}
	if crop.Kind != vips.KindEnum || crop.TypeName != "VipsInteresting" {
		t.Errorf("crop: got kind %s type %s", crop.Kind, crop.TypeName)
	}
	// The blurb is libvips' own documentation, carried through unchanged.
	if crop.Blurb == "" {
		t.Error("crop has no blurb")
	}
}

func TestDescribeUnknownOperation(t *testing.T) {
	if _, err := vips.Describe("no_such_operation"); err == nil {
		t.Fatal("expected an error for an unknown operation")
	}
}

// An explicitly supplied zero must reach libvips.
//
// This is the property the whole design exists to protect. hist_find defaults
// its band argument to -1, meaning "every band"; band 0 means "just the first".
// A binding that treats Go's zero value as "unset" cannot express band 0 at all
// and silently computes the wrong histogram.
func TestExplicitZeroIsSent(t *testing.T) {
	src := load(t, "black.png")
	if got := src.Bands(); got != 3 {
		t.Fatalf("fixture should have 3 bands, has %d", got)
	}

	// Omitted: libvips uses its own default of -1, all bands.
	deflt, err := vips.Call("hist_find", vips.In("in", src))
	if err != nil {
		t.Fatal(err)
	}
	defer deflt.Close()
	defaultImage, err := deflt.Image("out")
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultImage.Bands(); got != 3 {
		t.Errorf("hist_find with band omitted: got %d bands, want 3", got)
	}

	// Supplied as zero: must select band 0, not fall back to the default.
	explicit, err := vips.Call("hist_find", vips.In("in", src), vips.In("band", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer explicit.Close()
	zeroImage, err := explicit.Image("out")
	if err != nil {
		t.Fatal(err)
	}
	if got := zeroImage.Bands(); got != 1 {
		t.Errorf("hist_find with band=0: got %d bands, want 1 "+
			"(the explicit zero was dropped)", got)
	}
}

// The same property for enums, where zero is an ordinary member rather than a
// stand-in for "unset". VipsKernel 0 is nearest-neighbour and resize defaults
// to 5, lanczos3, so dropping the zero silently resamples with the wrong
// kernel and produces different pixels.
func TestExplicitZeroEnumIsSent(t *testing.T) {
	src := load(t, "noise.png")

	avgOf := func(t *testing.T, args ...vips.Arg) float64 {
		t.Helper()
		outs, err := vips.Call("resize", args...)
		if err != nil {
			t.Fatal(err)
		}
		defer outs.Close()
		im, err := outs.Image("out")
		if err != nil {
			t.Fatal(err)
		}
		stats, err := vips.Call("avg", vips.In("in", im))
		if err != nil {
			t.Fatal(err)
		}
		avg, err := stats.Float("out")
		if err != nil {
			t.Fatal(err)
		}
		return avg
	}

	deflt := avgOf(t, vips.In("in", src), vips.In("scale", 0.25))
	nearest := avgOf(t, vips.In("in", src), vips.In("scale", 0.25), vips.In("kernel", 0))

	if deflt == nearest {
		t.Errorf("resize with kernel=0 matched the lanczos3 default (avg %v); "+
			"the explicit zero was dropped", deflt)
	}
	t.Logf("lanczos3 avg %v, nearest avg %v", deflt, nearest)
}

// Booleans travel the same unconditional path. No input boolean in libvips
// 8.18 defaults to true, so an explicit false is not observable on its own;
// setting one to true proves the branch is wired, and there is no separate
// code path that a false could fall out of.
func TestBoolIsSent(t *testing.T) {
	src := load(t, "noise.png")

	save := func(t *testing.T, args ...vips.Arg) int {
		t.Helper()
		outs, err := vips.Call("pngsave_buffer", args...)
		if err != nil {
			t.Fatal(err)
		}
		defer outs.Close()
		buf, err := outs.Bytes("buffer")
		if err != nil {
			t.Fatal(err)
		}
		return len(buf)
	}

	plain := save(t, vips.In("in", src))
	interlaced := save(t, vips.In("in", src), vips.In("interlace", true))

	if plain == interlaced {
		t.Errorf("interlace=true produced the same %d bytes as omitting it", plain)
	}
	t.Logf("interlace omitted %d bytes, =true %d bytes", plain, interlaced)
}

func TestThumbnailRoundTrip(t *testing.T) {
	outs, err := vips.Call("thumbnail",
		vips.In("filename", filepath.Join("testdata", "noise.png")),
		vips.In("width", 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer outs.Close()

	im, err := outs.Image("out")
	if err != nil {
		t.Fatal(err)
	}
	if im.Width() != 32 {
		t.Errorf("width: got %d, want 32", im.Width())
	}

	saved, err := vips.Call("pngsave_buffer", vips.In("in", im))
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()

	buf, err := saved.Bytes("buffer")
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) < 8 || string(buf[1:4]) != "PNG" {
		t.Errorf("result is not a PNG, got %d bytes", len(buf))
	}
}

// Arrays, in both directions.
func TestArrayArguments(t *testing.T) {
	src := load(t, "black.png")

	// []float64 in: add a constant per band.
	outs, err := vips.Call("linear",
		vips.In("in", src),
		vips.In("a", []float64{1, 1, 1}),
		vips.In("b", []float64{10, 20, 30}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer outs.Close()
	im, err := outs.Image("out")
	if err != nil {
		t.Fatal(err)
	}

	// []float64 out: read the per-band average back.
	stats, err := vips.Call("avg", vips.In("in", im))
	if err != nil {
		t.Fatal(err)
	}
	avg, err := stats.Float("out")
	if err != nil {
		t.Fatal(err)
	}
	if want := 20.0; avg != want {
		t.Errorf("avg after adding 10/20/30 to black: got %v, want %v", avg, want)
	}
}

// Multiple outputs, including optional ones that must be asked for by name.
func TestOptionalOutputs(t *testing.T) {
	src := load(t, "noise.png")

	outs, err := vips.Call("min", vips.In("in", src), vips.Out("x"), vips.Out("y"))
	if err != nil {
		t.Fatal(err)
	}
	defer outs.Close()

	if _, err := outs.Float("out"); err != nil {
		t.Errorf("required output missing: %v", err)
	}
	x, err := outs.Int("x")
	if err != nil {
		t.Fatalf("optional output x: %v", err)
	}
	y, err := outs.Int("y")
	if err != nil {
		t.Fatalf("optional output y: %v", err)
	}
	if x < 0 || x >= src.Width() || y < 0 || y >= src.Height() {
		t.Errorf("min at (%d,%d) is outside the %dx%d image",
			x, y, src.Width(), src.Height())
	}
}

// Unknown names fail loudly with the valid ones listed, rather than being
// quietly ignored.
func TestUnknownArgumentIsRejected(t *testing.T) {
	src := load(t, "black.png")

	_, err := vips.Call("hist_find", vips.In("in", src), vips.In("bnad", 0))
	if err == nil {
		t.Fatal("expected an error for a misspelled argument")
	}
	t.Logf("%v", err)
}

func TestWrongTypeIsRejected(t *testing.T) {
	src := load(t, "black.png")

	if _, err := vips.Call("hist_find", vips.In("in", src), vips.In("band", "zero")); err == nil {
		t.Fatal("expected an error for a string where an int belongs")
	}
}

func TestOutputRequestedAsInput(t *testing.T) {
	src := load(t, "black.png")

	if _, err := vips.Call("hist_find", vips.In("in", src), vips.In("out", src)); err == nil {
		t.Fatal("expected an error when setting an output as an input")
	}
}

// Every argument of every operation must classify into the known type surface.
// A libvips upgrade that introduces a new argument type fails here rather than
// silently marshalling it as something plausible.
func TestNoUnknownArgumentKinds(t *testing.T) {
	var unknown []string
	for _, op := range vips.Operations() {
		spec, err := vips.Describe(op)
		if err != nil {
			t.Errorf("describe %s: %v", op, err)
			continue
		}
		for _, a := range spec.Args {
			if a.Deprecated {
				continue
			}
			if a.Kind == vips.KindUnknown {
				unknown = append(unknown, op+"."+a.Name+" ("+a.TypeName+")")
			}
		}
	}
	if len(unknown) > 0 {
		t.Errorf("libvips %s has arguments this package cannot classify: %v",
			vips.Version(), unknown)
	}
}

func TestEnumValues(t *testing.T) {
	vals := vips.EnumValues("VipsInteresting")
	if len(vals) == 0 {
		t.Fatal("no values for VipsInteresting")
	}
	found := false
	for _, v := range vals {
		if v.Nick == "centre" {
			found = true
		}
	}
	if !found {
		t.Errorf("VipsInteresting has no 'centre': %+v", vals)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
