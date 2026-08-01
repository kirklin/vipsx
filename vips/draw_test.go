package vips_test

import (
	"bytes"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// The regression this file exists for: draw operations modify their image in
// place inside libvips, and the image a caller passes may be shared with every
// other holder through the operation cache. Drawing on it directly once
// poisoned every later LoadFile of the same path. Call now substitutes a
// private copy and returns it, so the argument — and the cache — stay clean.
func TestDrawLeavesTheSharedImageAlone(t *testing.T) {
	vips.ClearCache()

	im := load(t, "noise.png")
	before, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}

	drawn, err := vips.DrawRect(im, []float64{255}, 0, 0, 8, 8,
		&vips.DrawRectOptions{Fill: vips.Ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer drawn.Close()

	after, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("DrawRect modified the image it was given")
	}

	drawnPixels, err := drawn.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, drawnPixels) {
		t.Error("DrawRect returned an image with nothing drawn on it")
	}

	// The cache half of the regression: a later load of the same path must
	// not see the rectangle.
	later := load(t, "noise.png")
	laterPixels, err := later.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, laterPixels) {
		t.Error("a later LoadFile of the same path returned the drawn-on image")
	}
}

// The same guarantee through the generic layer, which is where the copy is
// actually made: the returned Outputs carry the modified copy under the
// argument's name.
func TestCallReturnsTheModifiedCopy(t *testing.T) {
	im := load(t, "noise.png")

	outs, err := vips.Call("draw_rect",
		vips.In("image", im), vips.In("ink", []float64{255}),
		vips.In("left", 0), vips.In("top", 0),
		vips.In("width", 4), vips.In("height", 4),
		vips.In("fill", true))
	if err != nil {
		t.Fatal(err)
	}
	defer outs.Close()

	drawn, err := outs.Image("image")
	if err != nil {
		t.Fatalf("the modified copy is not in the outputs: %v", err)
	}
	if drawn == im {
		t.Fatal("Call handed back the argument itself rather than a copy")
	}
}

// Introspection has to say which arguments are modified, or neither the
// generator nor a caller of Describe can know to protect them. The flag was
// dropped entirely once.
func TestDescribeReportsModify(t *testing.T) {
	spec, err := vips.Describe("draw_rect")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := spec.Arg("image")
	if !ok {
		t.Fatal("draw_rect has no image argument?")
	}
	if !a.Modify {
		t.Error("draw_rect's image argument is not marked Modify")
	}

	spec, err = vips.Describe("gaussblur")
	if err != nil {
		t.Fatal(err)
	}
	if in, _ := spec.Arg("in"); in.Modify {
		t.Error("gaussblur's input is marked Modify, which it is not")
	}
}

// Optional outputs used to be unreachable from the typed layer: the options
// struct had no way to ask for them. Pointing a field at a variable is the
// request, and the answer arrives through it.
func TestOptionalOutputsArriveThroughOptions(t *testing.T) {
	// A size no other test uses: black images are shared through the
	// operation cache, and this test wants one nobody else has touched.
	im := black(t, 96, 96)
	defer im.Close()

	// A black image with one bright pixel drawn near the corner gives the
	// maximum a known position.
	marked, err := vips.DrawRect(im, []float64{255}, 5, 7, 1, 1,
		&vips.DrawRectOptions{Fill: vips.Ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	defer marked.Close()

	var x, y int
	max, err := vips.Max(marked, &vips.MaxOptions{X: &x, Y: &y})
	if err != nil {
		t.Fatal(err)
	}
	if max != 255 {
		t.Errorf("max: got %v, want 255", max)
	}
	if x != 5 || y != 7 {
		t.Errorf("maximum reported at (%d,%d), want (5,7)", x, y)
	}

	// Not requested means not touched.
	sentinelX := -1
	if _, err := vips.Max(marked, nil); err != nil {
		t.Fatal(err)
	}
	if sentinelX != -1 {
		t.Error("an unrequested slot changed")
	}
}
