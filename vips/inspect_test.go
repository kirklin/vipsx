package vips_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// The tag says which of eight orientations; this says the thing a caller
// actually needs, which is whether the sides are the wrong way round. Getting
// it wrong sizes a thumbnail against the stored dimensions of a photograph
// that displays rotated.
func TestOrientationSwaps(t *testing.T) {
	im := load(t, "noise.png")

	own, err := im.CopyMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer own.Close()

	if own.OrientationSwaps() {
		t.Error("an image with no orientation tag reports a swap")
	}

	// 1 through 4 keep the sides; 5 through 8 exchange them.
	for _, tc := range []struct {
		orientation int
		swap        bool
	}{{1, false}, {2, false}, {3, false}, {4, false}, {5, true}, {6, true}, {7, true}, {8, true}} {
		own.SetInt("orientation", tc.orientation)
		if got := own.OrientationSwaps(); got != tc.swap {
			t.Errorf("orientation %d: OrientationSwaps() = %v, want %v",
				tc.orientation, got, tc.swap)
		}
	}
}

func TestBandFormatPredicates(t *testing.T) {
	cases := []struct {
		format                             vips.BandFormat
		is8, isInt, isUint, isFloat, isCpx bool
	}{
		{vips.BandFormatUchar, true, true, true, false, false},
		{vips.BandFormatChar, true, true, false, false, false},
		{vips.BandFormatUshort, false, true, true, false, false},
		{vips.BandFormatFloat, false, false, false, true, false},
		{vips.BandFormatDouble, false, false, false, true, false},
		{vips.BandFormatComplex, false, false, false, false, true},
	}
	for _, tc := range cases {
		f := int(tc.format)
		if got := vips.Is8Bit(f); got != tc.is8 {
			t.Errorf("Is8Bit(%s) = %v, want %v", tc.format, got, tc.is8)
		}
		if got := vips.IsInt(f); got != tc.isInt {
			t.Errorf("IsInt(%s) = %v, want %v", tc.format, got, tc.isInt)
		}
		if got := vips.IsUint(f); got != tc.isUint {
			t.Errorf("IsUint(%s) = %v, want %v", tc.format, got, tc.isUint)
		}
		if got := vips.IsFloat(f); got != tc.isFloat {
			t.Errorf("IsFloat(%s) = %v, want %v", tc.format, got, tc.isFloat)
		}
		if got := vips.IsComplex(f); got != tc.isCpx {
			t.Errorf("IsComplex(%s) = %v, want %v", tc.format, got, tc.isCpx)
		}
	}

	// The reason Is8Bit is worth having: it is the question WriteToMemory's
	// result depends on.
	im := load(t, "noise.png")
	if !vips.Is8Bit(im.Format()) {
		t.Fatal("the test image is not 8-bit; the check below assumes it is")
	}
	raw, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != im.Width()*im.Height()*im.Bands() {
		t.Error("an 8-bit image did not produce one byte per band per pixel")
	}
}

func TestMaxAlpha(t *testing.T) {
	cases := map[vips.Interpretation]float64{
		vips.InterpretationSrgb:  255,
		vips.InterpretationRgb16: 65535,
		vips.InterpretationScrgb: 1,
	}
	major, minor, _ := vips.VersionParts()
	old := major < 8 || (major == 8 && minor < 15)

	for interp, want := range cases {
		got, err := vips.MaxAlpha(int(interp))
		if old {
			if err == nil {
				t.Errorf("MaxAlpha succeeded on libvips %s, which is older than 8.15", vips.Version())
			}
			continue
		}
		if err != nil {
			t.Errorf("MaxAlpha(%s): %v", interp, err)
			continue
		}
		if got != want {
			t.Errorf("MaxAlpha(%s) = %v, want %v", interp, got, want)
		}
	}
}

func TestMaxCoord(t *testing.T) {
	n := vips.MaxCoord()
	if n < 10000 {
		t.Fatalf("MaxCoord is %d, which is too small to be the real ceiling", n)
	}
	t.Logf("libvips accepts dimensions up to %d", n)

	// The point of knowing it: a request for something bigger can be refused
	// before any work happens.
	if im, err := vips.Black(n+1, 1, nil); err == nil {
		im.Close()
		t.Errorf("an image %d wide was created, past the reported ceiling", n+1)
	}
}

func TestSaveSuffixes(t *testing.T) {
	suffixes := vips.SaveSuffixes()
	if len(suffixes) == 0 {
		t.Fatal("no writable suffixes reported")
	}
	have := map[string]bool{}
	for _, s := range suffixes {
		if have[s] {
			t.Errorf("%s appears more than once", s)
		}
		have[s] = true
	}
	// Anything this suite writes has to be in the list, or the list is not
	// usable for validating a requested output format.
	for _, want := range []string{".png", ".jpg"} {
		if !have[want] {
			t.Errorf("%s is missing from SaveSuffixes, but this build writes it", want)
		}
	}
	t.Logf("%d writable suffixes: %s", len(suffixes),
		strings.Join(suffixes[:min(8, len(suffixes))], " "))
}

func TestLoaderFlags(t *testing.T) {
	path := filepath.Join("testdata", "noise.png")
	loader, err := vips.LoaderFor(path)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := vips.LoaderFlags(loader, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s on %s: partial=%v sequential=%v bigendian=%v",
		loader, filepath.Base(path), flags.Partial, flags.Sequential, flags.BigEndian)

	// A PNG is read top to bottom; whatever else is true, it is not partial
	// and sequential at once.
	if flags.Partial && flags.Sequential {
		t.Error("a loader reported both partial and sequential")
	}
}

func TestSplitFilename(t *testing.T) {
	cases := []struct {
		in, path, options string
	}{
		{"photo.jpg", "photo.jpg", ""},
		{"photo.jpg[Q=90]", "photo.jpg", "[Q=90]"},
		{"a/b/c.png[compression=9,strip]", "a/b/c.png", "[compression=9,strip]"},
	}
	for _, tc := range cases {
		path, options := vips.SplitFilename(tc.in)
		if path != tc.path || options != tc.options {
			t.Errorf("SplitFilename(%q) = %q, %q; want %q, %q",
				tc.in, path, options, tc.path, tc.options)
		}
		// libvips keeps the brackets, which makes the split reversible.
		if path+options != tc.in {
			t.Errorf("SplitFilename(%q) does not rejoin: %q + %q", tc.in, path, options)
		}
	}
}

func TestImageFilenameAndHistory(t *testing.T) {
	im := load(t, "noise.png")

	if name := im.Filename(); !strings.Contains(name, "noise.png") {
		t.Errorf("Filename() is %q, expected it to mention the file", name)
	}
	// History is whatever libvips recorded; the contract is that asking is
	// safe and answers a string, not that it says anything in particular.
	t.Logf("history: %q", im.History())
}

func TestGuessFormatAndInterpretation(t *testing.T) {
	im := load(t, "noise.png")
	if f := im.GuessFormat(); f < 0 {
		t.Errorf("GuessFormat returned %d", f)
	}
	if i := im.GuessInterpretation(); i < 0 {
		t.Errorf("GuessInterpretation returned %d", i)
	}
	t.Logf("guessed format %d, interpretation %d", im.GuessFormat(), im.GuessInterpretation())
}

func TestMinimiseAndInvalidateKeepTheImageUsable(t *testing.T) {
	im := load(t, "noise.png")
	before, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}

	im.Minimise()
	im.Invalidate()

	after, err := im.WriteToMemory()
	if err != nil {
		t.Fatalf("the image stopped working after Minimise and Invalidate: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the pixels changed across Minimise and Invalidate")
	}
}

func TestFreezeAndThawErrors(t *testing.T) {
	vips.FreezeErrors()
	_, frozen := vips.Call("jpegload", vips.In("filename", "/nonexistent/frozen.jpg"))
	vips.ThawErrors()

	if frozen == nil {
		t.Fatal("the call succeeded; it was supposed to fail")
	}
	// The call still fails — freezing hides the detail, not the failure.
	_, thawed := vips.Call("jpegload", vips.In("filename", "/nonexistent/thawed.jpg"))
	if thawed == nil {
		t.Fatal("the call succeeded after thawing")
	}
	if !strings.Contains(thawed.Error(), "thawed.jpg") {
		t.Errorf("after ThawErrors the detail is missing: %v", thawed)
	}
}

// ---------------------------------------------------------------------------
// Descriptor-backed sources and targets.
// ---------------------------------------------------------------------------

func TestSourceFromOpenFileMatchesTheFile(t *testing.T) {
	path := filepath.Join("testdata", "noise.png")

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	src, err := vips.NewSourceFromOpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	viaFd, err := vips.LoadSource(src)
	if err != nil {
		t.Fatal(err)
	}
	defer viaFd.Close()

	viaPath := load(t, "noise.png")

	a, err := viaFd.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	b, err := viaPath.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("reading through a descriptor gave different pixels than reading the path")
	}
}

func TestTargetToOpenFileWritesTheFile(t *testing.T) {
	im := load(t, "noise.png")

	path := filepath.Join(t.TempDir(), "out.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tg, err := vips.NewTargetToOpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	if err := vips.SaveTarget(im, tg, ".png"); err != nil {
		t.Fatal(err)
	}
	tg.Close()

	back, err := vips.LoadFile(path)
	if err != nil {
		t.Fatalf("what was written through the descriptor did not load back: %v", err)
	}
	defer back.Close()
	if back.Width() != im.Width() || back.Height() != im.Height() {
		t.Errorf("wrote %dx%d, read back %dx%d",
			im.Width(), im.Height(), back.Width(), back.Height())
	}
}

func TestNilFileIsRejected(t *testing.T) {
	if s, err := vips.NewSourceFromOpenFile(nil); err == nil {
		s.Close()
		t.Error("a nil file was accepted as a source")
	}
	if tg, err := vips.NewTargetToOpenFile(nil); err == nil {
		tg.Close()
		t.Error("a nil file was accepted as a target")
	}
}

func TestSourceSniffDoesNotConsume(t *testing.T) {
	raw := readTestdata(t, "noise.png")

	src, err := vips.NewSourceFromReader(sequentialOnly{bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	head, err := src.Sniff(8)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(head, raw[:8]) {
		t.Fatalf("sniffed % x, want % x", head, raw[:8])
	}

	// The point of sniffing rather than reading: the loader still gets the
	// whole thing.
	im, err := vips.LoadSource(src)
	if err != nil {
		t.Fatalf("loading after a sniff: %v", err)
	}
	defer im.Close()
	if im.Width() != 100 {
		t.Errorf("got a %d wide image after sniffing", im.Width())
	}
}

func TestSourceSniffRejectsMoreThanThereIs(t *testing.T) {
	src, err := vips.NewSourceFromBytes([]byte("tiny"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if b, err := src.Sniff(1 << 20); err == nil {
		t.Errorf("sniffing 1 MB from a 4-byte source returned %d bytes", len(b))
	}
	if _, err := src.Sniff(0); err == nil {
		t.Error("a zero-length sniff was accepted")
	}
}

func TestSourceLength(t *testing.T) {
	raw := readTestdata(t, "noise.png")

	src, err := vips.NewSourceFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if n := src.Length(); n != int64(len(raw)) {
		t.Errorf("Length() = %d, want %d", n, len(raw))
	}
}

func TestSourceMinimiseKeepsItUsable(t *testing.T) {
	path := filepath.Join("testdata", "noise.png")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	src, err := vips.NewSourceFromOpenFile(f)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	src.Minimise()
	src.Unminimise()

	im, err := vips.LoadSource(src)
	if err != nil {
		t.Fatalf("the source stopped working across Minimise: %v", err)
	}
	defer im.Close()
}

// ---------------------------------------------------------------------------
// Zero-copy pixels.
// ---------------------------------------------------------------------------

func TestWithPixelsSeesTheSameBytesAsWriteToMemory(t *testing.T) {
	im := load(t, "noise.png")

	want, err := im.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}

	var got []byte
	err = im.WithPixels(func(p []byte) error {
		// Copy inside the callback, which is what the doc requires: the slice
		// is the image's own memory and is not valid afterwards.
		got = append([]byte(nil), p...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("WithPixels and WriteToMemory disagree about the pixels")
	}
}

func TestWithPixelsPropagatesTheCallbackError(t *testing.T) {
	im := load(t, "noise.png")

	sentinel := &vips.Error{Op: "test", Message: "no"}
	if err := im.WithPixels(func([]byte) error { return sentinel }); err != sentinel {
		t.Fatalf("WithPixels returned %v, want the callback's error", err)
	}
	if err := im.WithPixels(nil); err == nil {
		t.Error("a nil callback was accepted")
	}
}
