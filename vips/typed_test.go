package vips_test

import (
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
