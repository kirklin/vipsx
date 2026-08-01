package vips_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The shape an image service wants: refuse everything, then name what it
// serves. These switches are process-wide, so the test puts them back.
func TestBlockOperationDeniesThenAllows(t *testing.T) {
	png := readTestdata(t, "noise.png")
	jpg := readTestdata(t, "noise.jpg")

	t.Cleanup(func() {
		if err := vips.BlockOperation("VipsForeignLoad", false); err != nil {
			t.Fatalf("could not unblock loaders: %v", err)
		}
	})

	if err := vips.BlockOperation("VipsForeignLoad", true); err != nil {
		t.Fatal(err)
	}
	if im, err := vips.LoadBuffer(png); err == nil {
		im.Close()
		t.Fatal("a PNG loaded while every loader was blocked")
	}
	if im, err := vips.LoadBuffer(jpg); err == nil {
		im.Close()
		t.Fatal("a JPEG loaded while every loader was blocked")
	}

	// Blocking covers a class and everything below it, so allowing one leaf
	// back has to work without disturbing the rest.
	if err := vips.BlockOperation("VipsForeignLoadJpeg", false); err != nil {
		t.Fatal(err)
	}
	im, err := vips.LoadBuffer(jpg)
	if err != nil {
		t.Fatalf("JPEG was allowed back but did not load: %v", err)
	}
	im.Close()

	if im, err := vips.LoadBuffer(png); err == nil {
		im.Close()
		t.Fatal("allowing JPEG also allowed PNG")
	}
}

func TestBlockUntrustedIsAccepted(t *testing.T) {
	t.Cleanup(func() {
		if err := vips.BlockUntrusted(false); err != nil {
			t.Fatal(err)
		}
	})
	if err := vips.BlockUntrusted(true); err != nil {
		t.Fatalf("BlockUntrusted: %v", err)
	}

	// Whatever else it blocks, the formats this suite uses are trusted and
	// must still load; otherwise the switch is not usable as a default.
	im, err := vips.LoadBuffer(readTestdata(t, "noise.jpg"))
	if err != nil {
		t.Fatalf("a JPEG failed with untrusted loaders blocked: %v", err)
	}
	im.Close()
}

func TestSetPipeReadLimitIsAccepted(t *testing.T) {
	// Restore libvips' actual default, 1 GB. It used to be put back at -1 —
	// no limit at all — which left the rest of the suite, and anyone copying
	// this test, looser than a process that never touched the knob.
	t.Cleanup(func() {
		if err := vips.SetPipeReadLimit(1 << 30); err != nil {
			t.Fatal(err)
		}
	})
	if err := vips.SetPipeReadLimit(64 << 20); err != nil {
		t.Fatalf("SetPipeReadLimit: %v", err)
	}
}
