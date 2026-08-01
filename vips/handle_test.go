package vips_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// black returns a small image that is not tied to a file, for the lifetime
// tests below: what is being tested is the handle, not the loader.
func black(t *testing.T, w, h int) *vips.Image {
	t.Helper()
	im, err := vips.Black(w, h, nil)
	if err != nil {
		t.Fatalf("black: %v", err)
	}
	return im
}

// A closed handle used to reach libvips as a NULL, which it dereferences: a
// SIGSEGV that takes the process down and names neither the image nor the
// caller. It has to be a Go panic, which a caller can survive and which points
// at the line that made the mistake.
func TestClosedImagePanicsRatherThanCrashing(t *testing.T) {
	cases := []struct {
		name string
		call func(im *vips.Image)
	}{
		{"Width", func(im *vips.Image) { im.Width() }},
		{"Height", func(im *vips.Image) { im.Height() }},
		{"Bands", func(im *vips.Image) { im.Bands() }},
		{"Format", func(im *vips.Image) { im.Format() }},
		{"Interpretation", func(im *vips.Image) { im.Interpretation() }},
		{"Coding", func(im *vips.Image) { im.Coding() }},
		{"Resolution", func(im *vips.Image) { im.Resolution() }},
		{"Offset", func(im *vips.Image) { im.Offset() }},
		{"HasAlpha", func(im *vips.Image) { im.HasAlpha() }},
		{"Fields", func(im *vips.Image) { im.Fields() }},
		{"HasField", func(im *vips.Image) { im.HasField("width") }},
		{"FieldKind", func(im *vips.Image) { im.FieldKind("width") }},
		{"GetInt", func(im *vips.Image) { im.GetInt("width") }},
		{"GetDouble", func(im *vips.Image) { im.GetDouble("xres") }},
		{"GetString", func(im *vips.Image) { im.GetString("filename") }},
		{"GetAsString", func(im *vips.Image) { im.GetAsString("width") }},
		{"GetBlob", func(im *vips.Image) { im.GetBlob("exif-data") }},
		{"GetInts", func(im *vips.Image) { im.GetInts("delay") }},
		{"GetFloats", func(im *vips.Image) { im.GetFloats("background") }},
		{"SetInt", func(im *vips.Image) { im.SetInt("orientation", 1) }},
		{"SetDouble", func(im *vips.Image) { im.SetDouble("xres", 1) }},
		{"SetString", func(im *vips.Image) { im.SetString("comment", "x") }},
		{"SetBlob", func(im *vips.Image) { im.SetBlob("blob", []byte("x")) }},
		{"SetInts", func(im *vips.Image) { im.SetInts("delay", []int{1}) }},
		{"SetFloats", func(im *vips.Image) { im.SetFloats("background", []float64{1}) }},
		{"RemoveField", func(im *vips.Image) { im.RemoveField("comment") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			im := black(t, 8, 8)
			im.Close()

			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s on a closed image did not panic", tc.name)
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("panicked with %T, want an error", r)
				}
				var closed *vips.ClosedError
				if !errors.As(err, &closed) {
					t.Fatalf("panicked with %v, want *vips.ClosedError", err)
				}
				if closed.Op != tc.name {
					t.Errorf("ClosedError names %q, want %q", closed.Op, tc.name)
				}
			}()
			tc.call(im)
		})
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	im := black(t, 8, 8)
	im.Close()
	im.Close()
	im.Close()
}

func TestCloseOnNilImageIsSafe(t *testing.T) {
	var im *vips.Image
	im.Close()
}

// Close writes the pointer and the accessors read it. Both go through an
// atomic, so the loser of the race gets a ClosedError rather than a read of
// freed memory. Run under -race, this is also what proves there is no data
// race left on the field itself.
func TestConcurrentCloseAndRead(t *testing.T) {
	const rounds = 200

	for i := 0; i < rounds; i++ {
		im := black(t, 16, 16)

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(4)

		go func() { defer wg.Done(); <-start; im.Close() }()
		go func() { defer wg.Done(); <-start; im.Close() }()
		for j := 0; j < 2; j++ {
			go func() {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						var closed *vips.ClosedError
						if err, ok := r.(error); !ok || !errors.As(err, &closed) {
							t.Errorf("reader panicked with %v, want *vips.ClosedError", r)
						}
					}
				}()
				<-start
				_ = im.Width()
				_ = im.Bands()
			}()
		}

		close(start)
		wg.Wait()
	}
}

// Marshalling turns a closed handle into a returned error naming the argument,
// rather than panicking: a call is a value the caller is expected to check.
func TestClosedImageAsArgumentIsAnError(t *testing.T) {
	im := black(t, 8, 8)
	im.Close()

	if _, err := vips.Call("invert", vips.In("in", im)); err == nil {
		t.Fatal("a closed image was accepted as an argument")
	}
}

// A typed nil is not a nil interface, so the handle check has to look through
// it. This used to dereference and panic instead of reporting the argument.
func TestTypedNilImageAsArgumentIsAnError(t *testing.T) {
	var im *vips.Image

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a typed-nil image panicked instead of erroring: %v", r)
		}
	}()

	if _, err := vips.Call("invert", vips.In("in", im)); err == nil {
		t.Fatal("a nil image was accepted as an argument")
	}
}

func TestClosedSourceAndTargetPanic(t *testing.T) {
	tg, err := vips.NewTargetToMemory()
	if err != nil {
		t.Fatal(err)
	}
	tg.Close()

	defer func() {
		r := recover()
		var closed *vips.ClosedError
		if err, ok := r.(error); !ok || !errors.As(err, &closed) {
			t.Fatalf("Bytes on a closed target gave %v, want *vips.ClosedError", r)
		}
	}()
	_, _ = tg.Bytes()
}

// The doc used to say Bytes could only be called once. It copies out of the
// target's blob rather than stealing it, so it can be called repeatedly and
// answers the same thing each time.
func TestTargetBytesIsRepeatable(t *testing.T) {
	im := black(t, 16, 16)
	defer im.Close()

	tg, err := vips.NewTargetToMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	if err := vips.PngsaveTarget(im, tg, nil); err != nil {
		t.Fatal(err)
	}

	first, err := tg.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	second, err := tg.Bytes()
	if err != nil {
		t.Fatalf("second Bytes failed: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("nothing written")
	}
	if len(first) != len(second) {
		t.Fatalf("Bytes returned %d then %d", len(first), len(second))
	}
}
