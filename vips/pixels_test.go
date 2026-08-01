package vips_test

import (
	"bytes"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

func TestFormatSizeof(t *testing.T) {
	cases := []struct {
		format vips.BandFormat
		want   int
	}{
		{vips.BandFormatUchar, 1},
		{vips.BandFormatUshort, 2},
		{vips.BandFormatUint, 4},
		{vips.BandFormatFloat, 4},
		{vips.BandFormatDouble, 8},
	}
	for _, tc := range cases {
		if got := vips.FormatSizeof(tc.format); got != tc.want {
			t.Errorf("FormatSizeof(%s) = %d, want %d", tc.format, got, tc.want)
		}
	}
}

// Pixels in, the same pixels out. The operation layer cannot do this at all:
// rawload only reads a filename, so without these the way in from memory was
// to write a temporary file.
func TestPixelsRoundTripThroughMemory(t *testing.T) {
	const w, h, bands = 4, 3, 3

	pix := make([]byte, w*h*bands)
	for i := range pix {
		pix[i] = byte(i * 7)
	}

	im, err := vips.NewImageFromMemory(pix, w, h, bands, vips.BandFormatUchar)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	if im.Width() != w || im.Height() != h || im.Bands() != bands {
		t.Fatalf("got %dx%d with %d bands, want %dx%d with %d",
			im.Width(), im.Height(), im.Bands(), w, h, bands)
	}

	out, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pix, out) {
		t.Fatalf("pixels changed in the round trip:\n in: %v\nout: %v", pix, out)
	}
}

// libvips takes its own copy, so the caller's slice is free immediately. If it
// ever stopped copying, this would read whatever the slice was overwritten
// with.
func TestNewImageFromMemoryCopiesTheBuffer(t *testing.T) {
	const w, h, bands = 8, 8, 1

	pix := bytes.Repeat([]byte{0x42}, w*h*bands)
	im, err := vips.NewImageFromMemory(pix, w, h, bands, vips.BandFormatUchar)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	for i := range pix {
		pix[i] = 0xFF
	}

	out, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range out {
		if b != 0x42 {
			t.Fatalf("byte %d is %#x, want 0x42: the buffer was not copied", i, b)
		}
	}
}

func TestNewImageFromMemoryRejectsAShortBuffer(t *testing.T) {
	short := make([]byte, 10)
	if im, err := vips.NewImageFromMemory(short, 100, 100, 3, vips.BandFormatUchar); err == nil {
		im.Close()
		t.Fatal("a buffer far too small for the dimensions was accepted")
	}
}

func TestNewImageFromMemoryRejectsBadDimensions(t *testing.T) {
	pix := make([]byte, 64)
	for _, tc := range []struct{ w, h, bands int }{{0, 8, 1}, {8, 0, 1}, {8, 8, 0}, {-1, 8, 1}} {
		if im, err := vips.NewImageFromMemory(pix, tc.w, tc.h, tc.bands, vips.BandFormatUchar); err == nil {
			im.Close()
			t.Errorf("%dx%d with %d bands was accepted", tc.w, tc.h, tc.bands)
		}
	}
}

func TestWriteToMemoryIsRawPixelsNotAnEncoding(t *testing.T) {
	im := load(t, "noise.png")

	raw, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	want := im.Width() * im.Height() * im.Bands() * vips.FormatSizeof(vips.BandFormat(im.Format()))
	if len(raw) != want {
		t.Fatalf("got %d bytes, want %d for %dx%d with %d bands",
			len(raw), want, im.Width(), im.Height(), im.Bands())
	}
	if bytes.HasPrefix(raw, []byte("\x89PNG")) {
		t.Fatal("WriteToMemory returned an encoded image, not pixels")
	}
}

// The scenario CopyMemory exists for. A reader-backed source releases its
// reader at Close, and an image that has not been evaluated yet still needs it:
// closing first used to fail the save with libvips' generic read error, which
// does not say what went wrong. Materialising cuts the tie.
func TestCopyMemoryOutlivesItsSource(t *testing.T) {
	jpg := readTestdata(t, "noise.jpg")

	src, err := vips.NewSourceFromReader(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	im, err := vips.JpegloadSource(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	own, err := im.CopyMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()

	// The reader goes back to its owner here, before anything is saved.
	src.Close()

	buf, err := vips.SaveBuffer(own, ".png")
	if err != nil {
		t.Fatalf("saving a materialised image after closing its source: %v", err)
	}
	if len(buf) == 0 {
		t.Fatal("nothing was written")
	}
}

// The same sequence without CopyMemory is what makes the test above worth
// having: it has to fail, or CopyMemory is solving nothing.
func TestLazyImageDoesNotOutliveItsSource(t *testing.T) {
	jpg := readTestdata(t, "noise.jpg")

	src, err := vips.NewSourceFromReader(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	im, err := vips.JpegloadSource(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	src.Close()

	if _, err := vips.SaveBuffer(im, ".png"); err == nil {
		t.Fatal("a lazy image was saved after its source was closed; " +
			"if libvips has changed here, TestCopyMemoryOutlivesItsSource needs revisiting")
	}
}

func TestCopyMemoryIsIndependentOfTheOriginal(t *testing.T) {
	im := load(t, "noise.png")

	own, err := im.CopyMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()

	if own.Width() != im.Width() || own.Height() != im.Height() {
		t.Fatalf("copy is %dx%d, original is %dx%d",
			own.Width(), own.Height(), im.Width(), im.Height())
	}

	a, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	b, err := own.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("CopyMemory changed the pixels")
	}
}
