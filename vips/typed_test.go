package vips_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

func loadTyped(t *testing.T, name string) *vips.Image {
	t.Helper()
	im, err := vips.LoadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	t.Cleanup(im.Close)
	return im
}

func TestTypedBasics(t *testing.T) {
	src := loadTyped(t, "noise.png")

	small, err := vips.Resize(src, 0.5, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()

	if got, want := small.Width(), src.Width()/2; got != want {
		t.Errorf("width: got %d, want %d", got, want)
	}

	blurred, err := vips.Gaussblur(small, 2.0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blurred.Close()

	buf, err := vips.SaveBuffer(blurred, ".png")
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) < 8 || string(buf[1:4]) != "PNG" {
		t.Errorf("not a PNG, %d bytes", len(buf))
	}
}

// The typed layer must preserve the property the whole design rests on. A nil
// option field means "not supplied"; a pointer to the zero value means "set it
// to zero", and the two must reach libvips differently.
func TestTypedExplicitZeroSurvives(t *testing.T) {
	src := loadTyped(t, "noise.png")

	avgOf := func(t *testing.T, im *vips.Image) float64 {
		t.Helper()
		v, err := vips.Avg(im)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// Kernel omitted: libvips uses lanczos3.
	deflt, err := vips.Resize(src, 0.25, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deflt.Close()

	// KernelNearest is zero. A binding that treated zero as "unset" would
	// silently resample with lanczos3 here.
	nearest, err := vips.Resize(src, 0.25, &vips.ResizeOptions{
		Kernel: vips.Ptr(vips.KernelNearest),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nearest.Close()

	a, b := avgOf(t, deflt), avgOf(t, nearest)
	if a == b {
		t.Errorf("Kernel: Ptr(KernelNearest) matched the default (both avg %v); "+
			"the explicit zero was dropped", a)
	}
	t.Logf("lanczos3 avg %v, nearest avg %v", a, b)
}

func TestTypedExplicitZeroInt(t *testing.T) {
	src := loadTyped(t, "black.png")
	if src.Bands() != 3 {
		t.Fatalf("fixture should have 3 bands, has %d", src.Bands())
	}

	all, err := vips.HistFind(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer all.Close()
	if got := all.Bands(); got != 3 {
		t.Errorf("Band omitted: got %d bands, want 3", got)
	}

	one, err := vips.HistFind(src, &vips.HistFindOptions{Band: vips.Ptr(0)})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	if got := one.Bands(); got != 1 {
		t.Errorf("Band: Ptr(0): got %d bands, want 1", got)
	}
}

// Enums carry libvips' own nicknames, so a failed assertion reads properly.
func TestGeneratedEnums(t *testing.T) {
	if got := vips.KernelNearest.String(); got != "nearest" {
		t.Errorf("KernelNearest: got %q", got)
	}
	if got := vips.InterestingCentre.String(); got != "centre" {
		t.Errorf("InterestingCentre: got %q", got)
	}
	if vips.KernelLanczos3 == vips.KernelNearest {
		t.Error("distinct kernels compare equal")
	}
}

// Operations with several outputs return them all.
func TestTypedMultipleOutputs(t *testing.T) {
	src := loadTyped(t, "noise.png")

	v, err := vips.Min(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v < 0 || v > 255 {
		t.Errorf("min out of range: %v", v)
	}
}

// Operations that write rather than return an image come back as a bare error.
func TestTypedSaveHasNoImageOutput(t *testing.T) {
	src := loadTyped(t, "noise.png")
	out := filepath.Join(t.TempDir(), "out.png")

	if err := vips.Pngsave(src, out, nil); err != nil {
		t.Fatal(err)
	}
	if im, err := vips.LoadFile(out); err != nil {
		t.Fatalf("saved file does not load back: %v", err)
	} else {
		im.Close()
	}
}

// Interpolators, sources and targets reach the typed layer as real types.
func TestTypedHandles(t *testing.T) {
	src := loadTyped(t, "noise.png")

	interp, err := vips.NewInterpolate("bicubic")
	if err != nil {
		t.Fatal(err)
	}
	defer interp.Close()

	out, err := vips.Affine(src, []float64{0.5, 0, 0, 0.5}, &vips.AffineOptions{
		Interpolate: vips.Ptr(interp),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	if out.Width() != src.Width()/2 {
		t.Errorf("affine width: got %d, want %d", out.Width(), src.Width()/2)
	}
}

func TestTypedSourceTarget(t *testing.T) {
	src, err := vips.NewSourceFromFile(filepath.Join("testdata", "noise.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	im, err := vips.PngloadSource(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	target, err := vips.NewTargetToMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := vips.WebpsaveTarget(im, target, nil); err != nil {
		t.Fatal(err)
	}
	buf, err := target.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) == 0 {
		t.Fatal("memory target produced nothing")
	}
	t.Logf("%d bytes of webp through a memory target", len(buf))
}

func TestMetadata(t *testing.T) {
	src := loadTyped(t, "noise.png")

	fields := src.Fields()
	if len(fields) == 0 {
		t.Fatal("no metadata fields")
	}

	src.SetString("vipsx-test", "hello")
	if v, err := src.GetString("vipsx-test"); err != nil || v != "hello" {
		t.Errorf("string round trip: %q %v", v, err)
	}
	src.SetInt("vipsx-int", 0)
	if v, err := src.GetInt("vipsx-int"); err != nil || v != 0 {
		t.Errorf("int round trip: %v %v", v, err)
	}
	if !src.RemoveField("vipsx-test") {
		t.Error("RemoveField reported nothing removed")
	}
	if src.HasField("vipsx-test") {
		t.Error("field survived removal")
	}

	if src.Orientation() < 1 {
		t.Errorf("orientation should default to 1, got %d", src.Orientation())
	}
	if src.Pages() != 1 {
		t.Errorf("pages: got %d, want 1", src.Pages())
	}
}

// Every name this package hands out is an operation nickname, the spelling
// Operations and Describe use. libvips answers the loader lookup with a class
// name instead, and both work when calling, so the mismatch was invisible until
// something tried to compare the two.
func TestLookupsReturnOperationNames(t *testing.T) {
	known := map[string]bool{}
	for _, op := range vips.Operations() {
		known[op] = true
	}

	src := filepath.Join("testdata", "noise.png")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	lookups := []struct {
		what string
		get  func() (string, error)
	}{
		{"LoaderFor", func() (string, error) { return vips.LoaderFor(src) }},
		{"LoaderForBuffer", func() (string, error) { return vips.LoaderForBuffer(data) }},
		{"SaverFor", func() (string, error) { return vips.SaverFor("out.webp") }},
		{"SaverForBuffer", func() (string, error) { return vips.SaverForBuffer(".jpg") }},
	}

	for _, l := range lookups {
		name, err := l.get()
		if err != nil {
			t.Errorf("%s: %v", l.what, err)
			continue
		}
		if !known[name] {
			t.Errorf("%s returned %q, which is not an operation name; "+
				"it cannot be compared against Operations or Describe", l.what, name)
		}
		if _, err := vips.Describe(name); err != nil {
			t.Errorf("%s returned %q, which Describe rejects: %v", l.what, name, err)
		}
	}

	// The specific case that started this: a HEIC file.
	if name, err := vips.LoaderFor(src); err == nil && name != "pngload" {
		t.Errorf("LoaderFor on a PNG: got %q, want pngload", name)
	}
}

// EXIF reports tags. The undecoded EXIF segment shares their prefix and is a
// few kilobytes of binary, which rendered as text is thousands of characters of
// base64 sitting in the map beside the real tags.
func TestEXIFExcludesTheRawBlock(t *testing.T) {
	src := loadTyped(t, "noise.png")

	// The fixture carries no EXIF, so give it some, including a raw block.
	own, err := vips.Copy(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()

	own.SetString("exif-ifd0-Make", "ACME (ACME, ASCII, 4 components, 4 bytes)")
	own.SetBlob("exif-data", make([]byte, 4096))

	exif := own.EXIF()
	if _, ok := exif["exif-data"]; ok {
		t.Error("EXIF included the raw exif-data block, which is not a tag")
	}
	if got := exif["exif-ifd0-Make"]; got == "" {
		t.Error("EXIF dropped a real tag")
	}
	for name, value := range exif {
		if len(value) > 2048 {
			t.Errorf("EXIF value for %q is %d characters; a tag should not be that large",
				name, len(value))
		}
	}

	// The bytes are still reachable, just not pretending to be a tag.
	if !own.HasEXIF() {
		t.Error("HasEXIF should still see the block")
	}
	blob, err := own.GetBlob("exif-data")
	if err != nil {
		t.Fatalf("GetBlob should still return the raw block: %v", err)
	}
	if len(blob) != 4096 {
		t.Errorf("raw block: got %d bytes, want 4096", len(blob))
	}
}

// Strip removes what an image says about itself and leaves what it is.
func TestStripRemovesMetadataNotPixels(t *testing.T) {
	src := loadTyped(t, "noise.png")

	// Give it something to strip, on a copy so the fixture is untouched.
	subject, err := vips.Copy(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer subject.Close()
	subject.SetBlob("exif-data", make([]byte, 2048))
	subject.SetString("exif-ifd0-Make", "ACME")
	subject.SetBlob("icc-profile-data", make([]byte, 512))

	before := subject.MetadataFields()
	if len(before) < 3 {
		t.Fatalf("expected metadata to strip, found %v", before)
	}

	stripped, err := subject.Strip()
	if err != nil {
		t.Fatal(err)
	}
	defer stripped.Close()

	if got := stripped.MetadataFields(); len(got) != 0 {
		t.Errorf("Strip left %v", got)
	}
	if stripped.HasEXIF() || stripped.HasProfile() {
		t.Error("Strip left EXIF or an ICC profile behind")
	}
	// Pixels, not encoded bytes: a PNG carries the metadata inside it, so the
	// files differ by design. Subtracting the two images is the only comparison
	// that answers the question actually being asked.
	diff, err := vips.Subtract(subject, stripped)
	if err != nil {
		t.Fatal(err)
	}
	defer diff.Close()
	magnitude, err := vips.Abs(diff)
	if err != nil {
		t.Fatal(err)
	}
	defer magnitude.Close()
	worst, err := vips.Max(magnitude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if worst != 0 {
		t.Errorf("Strip changed the pixels, by as much as %v", worst)
	}

	// The original is untouched: Strip copies rather than mutating, because an
	// image from an operation may be shared through the cache.
	if len(subject.MetadataFields()) != len(before) {
		t.Error("Strip modified the image it was called on")
	}
}

func TestStripIsSelective(t *testing.T) {
	src := loadTyped(t, "noise.png")
	subject, err := vips.Copy(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer subject.Close()
	subject.SetBlob("exif-data", make([]byte, 128))
	subject.SetBlob("icc-profile-data", make([]byte, 128))

	partial, err := subject.Strip("exif-data")
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()

	if partial.HasEXIF() {
		t.Error("the named field survived")
	}
	if !partial.HasProfile() {
		t.Error("an unnamed field was removed as well")
	}
}

// Fields describing the pixels are refused rather than silently ignored, since
// removing one either fails inside libvips or changes what the image is.
func TestStripRefusesStructuralFields(t *testing.T) {
	src := loadTyped(t, "noise.png")
	for _, name := range []string{"width", "height", "bands", "interpretation"} {
		if _, err := src.Strip(name); err == nil {
			t.Errorf("Strip(%q) should have been refused", name)
		}
	}
}

// Animation timing is structural: an image stripped of its provenance must
// still play at the right speed, the same way it must still know its page
// height. The fields are set by hand because that is what gifload sets, minus
// the dependency on a GIF encoder being compiled in.
func TestStripKeepsAnimationTiming(t *testing.T) {
	im := loadTyped(t, "noise.png")
	im.SetInts("delay", []int{40, 40, 40})
	im.SetInt("loop", 3)
	im.SetInt("gif-delay", 4)
	im.SetString("comment", "shot on a potato")

	bare, err := im.Strip()
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Close()

	if got, err := bare.GetInts("delay"); err != nil || len(got) != 3 {
		t.Errorf("Strip removed delay (%v, %v); the animation would play at the wrong speed",
			got, err)
	}
	if got, err := bare.GetInt("loop"); err != nil || got != 3 {
		t.Errorf("Strip removed or changed loop: got %d, %v", got, err)
	}
	if !bare.HasField("gif-delay") {
		t.Error("Strip removed gif-delay, the legacy spelling")
	}
	if bare.HasField("comment") {
		t.Error("comment survived Strip; only structural fields should")
	}
}
