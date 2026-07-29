package vips_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// sequentialOnly hides the Seek method of whatever it wraps, so libvips is
// given the same thing an HTTP body or a pipe would be.
type sequentialOnly struct{ r io.Reader }

func (s sequentialOnly) Read(p []byte) (int, error) { return s.r.Read(p) }

// failingReader refuses immediately, to check that the reason reaches the
// caller rather than being flattened into a libvips message.
//
// Immediately, not after a few bytes: a loader handed bytes that are not the
// format it expects gives up on its own, and never asks again, so the reader's
// error is never the thing that stopped it. That passed on one platform and
// failed on another until the reader was made to refuse from the start.
type failingReader struct {
	left int
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, f.err
	}
	n := min(len(p), f.left)
	for i := range n {
		p[i] = 0
	}
	f.left -= n
	return n, nil
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// pixelsOf renders an image to PNG so two paths can be compared byte for byte.
func pixelsOf(t *testing.T, im *vips.Image) []byte {
	t.Helper()
	buf, err := vips.SaveBuffer(im, ".png")
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

// Reading through an io.Reader must give the same image as reading the file,
// whether or not the reader can seek. The file path is the reference: it is the
// one already checked against the vips command line.
func TestSourceFromReaderMatchesTheFile(t *testing.T) {
	path := filepath.Join("testdata", "noise.png")

	reference, err := vips.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Close()
	want := pixelsOf(t, reference)

	for _, tc := range []struct {
		name     string
		wrap     func(*os.File) io.Reader
		seekable bool
	}{
		{"seekable", func(f *os.File) io.Reader { return f }, true},
		{"sequential", func(f *os.File) io.Reader { return sequentialOnly{f} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			src, err := vips.NewSourceFromReader(tc.wrap(f))
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()

			im, err := vips.PngloadSource(src, nil)
			if err != nil {
				t.Fatalf("reading through a %s reader: %v", tc.name, err)
			}
			defer im.Close()

			if got := pixelsOf(t, im); !bytes.Equal(got, want) {
				t.Errorf("a %s reader produced different pixels than the same file",
					tc.name)
			}
			if err := src.Err(); err != nil {
				t.Errorf("the reader reported %v", err)
			}
		})
	}
}

// Writing through an io.Writer must produce the same bytes as writing to a
// buffer, which is the path already compared against the command line.
func TestTargetToWriterMatchesTheBuffer(t *testing.T) {
	src := loadTyped(t, "noise.png")

	want, err := vips.SaveBuffer(src, ".jpg", vips.In("Q", 80))
	if err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	target, err := vips.NewTargetToWriter(&got)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := vips.JpegsaveTarget(src, target, &vips.JpegsaveTargetOptions{
		Q: vips.Ptr(80),
	}); err != nil {
		t.Fatal(err)
	}
	if err := target.Err(); err != nil {
		t.Fatalf("the writer reported %v", err)
	}

	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("writing to an io.Writer gave %d bytes, writing to a buffer gave %d",
			got.Len(), len(want))
	}
}

// A bufio.Writer is flushed by the end signal, so callers do not have to.
func TestTargetFlushesABufferedWriter(t *testing.T) {
	src := loadTyped(t, "noise.png")

	var sink bytes.Buffer
	buffered := bufio.NewWriterSize(&sink, 1<<16)

	target, err := vips.NewTargetToWriter(buffered)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := vips.JpegsaveTarget(src, target, nil); err != nil {
		t.Fatal(err)
	}
	if sink.Len() == 0 {
		t.Fatal("nothing reached the underlying writer; the buffer was not flushed")
	}
	if got := sink.Bytes(); got[0] != 0xFF || got[1] != 0xD8 {
		t.Errorf("not a JPEG: % x", got[:min(len(got), 4)])
	}
}

// A reader that fails partway must fail the load, and the reason must survive.
func TestStreamErrorsReachTheCaller(t *testing.T) {
	sentinel := errors.New("the network went away")

	src, err := vips.NewSourceFromReader(&failingReader{left: 0, err: sentinel})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if _, err := vips.PngloadSource(src, nil); err == nil {
		t.Fatal("expected the load to fail")
	}
	if got := src.Err(); !errors.Is(got, sentinel) {
		t.Errorf("Source.Err: got %v, want %v", got, sentinel)
	}

	target, err := vips.NewTargetToWriter(failingWriter{err: sentinel})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	im := loadTyped(t, "noise.png")
	if err := vips.JpegsaveTarget(im, target, nil); err == nil {
		t.Fatal("expected the save to fail")
	}
	if got := target.Err(); !errors.Is(got, sentinel) {
		t.Errorf("Target.Err: got %v, want %v", got, sentinel)
	}
}

// The shape a service actually uses: bytes in, bytes out, nothing touching disk.
func TestReaderToWriterRoundTrip(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "noise.png"))
	if err != nil {
		t.Fatal(err)
	}

	src, err := vips.NewSourceFromReader(bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	im, err := vips.PngloadSource(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	small, err := vips.ThumbnailImage(im, 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()

	var out bytes.Buffer
	target, err := vips.NewTargetToWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := vips.WebpsaveTarget(small, target, nil); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("nothing was written")
	}

	back, err := vips.LoadBuffer(out.Bytes())
	if err != nil {
		t.Fatalf("the result does not load back: %v", err)
	}
	defer back.Close()
	if back.Width() != 32 {
		t.Errorf("round trip width: got %d, want 32", back.Width())
	}
}

// The reason must survive Close. A caller that defers Close and checks Err on
// the way out is the ordinary shape, not a mistake, and the error it is owed
// must not vanish because the handle went first.
func TestStreamErrSurvivesClose(t *testing.T) {
	sentinel := errors.New("disk full")

	target, err := vips.NewTargetToWriter(failingWriter{err: sentinel})
	if err != nil {
		t.Fatal(err)
	}
	im := loadTyped(t, "noise.png")
	if err := vips.JpegsaveTarget(im, target, nil); err == nil {
		t.Fatal("expected the save to fail")
	}
	target.Close()
	if got := target.Err(); !errors.Is(got, sentinel) {
		t.Errorf("Target.Err after Close: got %v, want %v", got, sentinel)
	}

	src, err := vips.NewSourceFromReader(&failingReader{left: 0, err: sentinel})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vips.PngloadSource(src, nil); err == nil {
		t.Fatal("expected the load to fail")
	}
	src.Close()
	if got := src.Err(); !errors.Is(got, sentinel) {
		t.Errorf("Source.Err after Close: got %v, want %v", got, sentinel)
	}
}

// Closing the source before an image loaded from it has been evaluated severs
// the stream: the reader is released at Close, deterministically, and an
// operation that demands bytes afterwards fails cleanly rather than reaching a
// reader the caller was told is free. The other lifetime — keeping the entry
// until libvips drops the object — was tried and measured: evaluating an image
// drags internal operations into the libvips operation cache, which then pins
// the reader until an eviction that may never come.
func TestCloseSeversTheStream(t *testing.T) {
	// Big enough that loading the header cannot have buffered the whole file,
	// or the save below would succeed from memory and prove nothing.
	noise, err := vips.Gaussnoise(600, 600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer noise.Close()
	path := filepath.Join(t.TempDir(), "big.png")
	if err := vips.Pngsave(noise, path, nil); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	before := vips.OpenStreams()
	src, err := vips.NewSourceFromReader(f)
	if err != nil {
		t.Fatal(err)
	}
	im, err := vips.PngloadSource(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	src.Close()
	if n := vips.OpenStreams(); n != before {
		t.Errorf("the reader is still registered after Close: %d streams, want %d",
			n, before)
	}
	if _, err := vips.SaveBuffer(im, ".png"); err == nil {
		t.Error("evaluating after Close should fail; the reader was released at Close")
	}
}

// Closing must release the registry entry, or every stream ever opened stays
// reachable for the life of the process along with its reader.
func TestClosingAStreamReleasesIt(t *testing.T) {
	path := filepath.Join("testdata", "noise.png")
	for range 200 {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		src, err := vips.NewSourceFromReader(f)
		if err != nil {
			f.Close()
			t.Fatal(err)
		}
		im, err := vips.PngloadSource(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		im.Close()
		src.Close()
		f.Close()
	}
	if n := vips.OpenStreams(); n != 0 {
		t.Errorf("%d stream registrations survived their Close", n)
	}
}
