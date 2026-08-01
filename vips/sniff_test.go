package vips_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// A file or a buffer could be sniffed; a source could not. Streaming was the
// one path where the caller had to know the format up front and name the
// loader — and an HTTP body is where that knowledge is least available.
func TestLoadSourceSniffsTheFormat(t *testing.T) {
	cases := []struct {
		file   string
		loader string
	}{
		{"noise.jpg", "jpegload_source"},
		{"noise.png", "pngload_source"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw := readTestdata(t, tc.file)

			// sequentialOnly hides Seek, which is the shape of a request body:
			// libvips has to buffer to sniff, then read the same bytes again.
			src, err := vips.NewSourceFromReader(sequentialOnly{bytes.NewReader(raw)})
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()

			loader, err := vips.LoaderForSource(src)
			if err != nil {
				t.Fatal(err)
			}
			if loader != tc.loader {
				t.Errorf("sniffed %q, want %q", loader, tc.loader)
			}

			// Sniffing has to leave the source where the loader can still use
			// it; otherwise LoaderForSource would be a trap rather than a tool.
			im, err := vips.LoadSource(src)
			if err != nil {
				t.Fatalf("loading after sniffing: %v", err)
			}
			defer im.Close()

			if im.Width() != 100 || im.Height() != 80 {
				t.Errorf("got %dx%d, want 100x80", im.Width(), im.Height())
			}
		})
	}
}

func TestLoadSourceMatchesLoadBuffer(t *testing.T) {
	raw := readTestdata(t, "noise.png")

	viaBuffer, err := vips.LoadBuffer(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer viaBuffer.Close()

	src, err := vips.NewSourceFromReader(sequentialOnly{bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	viaSource, err := vips.LoadSource(src)
	if err != nil {
		t.Fatal(err)
	}
	defer viaSource.Close()

	a, err := viaBuffer.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	b, err := viaSource.WriteToMemory()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("the same bytes read through a source and a buffer gave different pixels")
	}
}

func TestLoadSourceRejectsSomethingThatIsNotAnImage(t *testing.T) {
	src, err := vips.NewSourceFromReader(strings.NewReader("this is not an image"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if im, err := vips.LoadSource(src); err == nil {
		im.Close()
		t.Fatal("plain text was accepted as an image")
	}
}

func TestSaverForTarget(t *testing.T) {
	cases := map[string]string{
		".webp": "webpsave_target",
		".png":  "pngsave_target",
		".jpg":  "jpegsave_target",
	}
	for suffix, want := range cases {
		got, err := vips.SaverForTarget(suffix)
		if err != nil {
			t.Errorf("SaverForTarget(%q): %v", suffix, err)
			continue
		}
		if got != want {
			t.Errorf("SaverForTarget(%q) = %q, want %q", suffix, got, want)
		}
	}
}

func TestSaveTargetWritesTheChosenFormat(t *testing.T) {
	im := load(t, "noise.png")

	var out bytes.Buffer
	tg, err := vips.NewTargetToWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	if err := vips.SaveTarget(im, tg, ".webp", vips.In("Q", 80)); err != nil {
		t.Fatal(err)
	}
	if err := tg.Err(); err != nil {
		t.Fatalf("the writer reported %v", err)
	}

	got := out.Bytes()
	if len(got) == 0 {
		t.Fatal("nothing was written")
	}
	// RIFF....WEBP
	if len(got) < 12 || !bytes.Equal(got[:4], []byte("RIFF")) || !bytes.Equal(got[8:12], []byte("WEBP")) {
		t.Fatalf("what was written is not a WebP: % x", got[:min(16, len(got))])
	}
}

// The round trip a proxy actually performs: read a body it cannot seek, do not
// know the format, write the answer straight out.
func TestSourceToTargetWithoutKnowingTheFormat(t *testing.T) {
	raw := readTestdata(t, "noise.jpg")

	src, err := vips.NewSourceFromReader(sequentialOnly{bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	im, err := vips.LoadSource(src)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	thumb, err := vips.ThumbnailImage(im, 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer thumb.Close()

	var out bytes.Buffer
	tg, err := vips.NewTargetToWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	if err := vips.SaveTarget(thumb, tg, ".png"); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("nothing was written")
	}

	back, err := vips.LoadBuffer(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	if back.Width() != 32 {
		t.Errorf("the thumbnail came back %d wide, want 32", back.Width())
	}
}
