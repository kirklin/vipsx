// Package soak checks that a long run of work does not accumulate memory or
// file descriptors on the libvips side.
//
// Go's own tooling cannot see this. The race detector finds data races and the
// heap profiler accounts for Go allocations, but every pixel buffer here lives
// in C, reference-counted by GObject. What this package watches is
// vips_tracked_get_mem and friends, which is libvips reporting on itself.
//
// The failure this is aimed at is real and was hit during development: an image
// handle that both Close and the garbage collector released, which is a
// double free rather than a leak, and a leak is the same bug with the sign
// flipped. Neither shows up in a short test.
package soak

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/kirklin/vipsx/vips"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.png")
	cmd := exec.Command("vips", "gaussnoise", path, "320", "240", "--seed", "7")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build a fixture without the vips CLI: %v\n%s", err, out)
	}
	rgb := filepath.Join(dir, "rgb.png")
	cmd = exec.Command("vips", "bandjoin_const", path, rgb, "100 180")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return rgb
}

// pipeline is one unit of work: everything a real caller does in a request.
// Loading, several operations, metadata, and an encode.
func pipeline(path string) error {
	im, err := vips.LoadFile(path)
	if err != nil {
		return err
	}
	defer im.Close()

	small, err := vips.Resize(im, 0.5, &vips.ResizeOptions{
		Kernel: vips.Ptr(vips.KernelNearest), // an explicit zero, every time
	})
	if err != nil {
		return err
	}
	defer small.Close()

	blurred, err := vips.Gaussblur(small, 1.5, nil)
	if err != nil {
		return err
	}
	defer blurred.Close()

	gray, err := vips.Colourspace(blurred, vips.InterpretationBW, nil)
	if err != nil {
		return err
	}
	defer gray.Close()

	// Touch metadata, which allocates on the C side too.
	//
	// On a private copy, not on gray: gray came out of an operation, and the
	// operation cache hands the same object to every caller that asks for the
	// same thing. Mutating a shared header from several goroutines corrupts the
	// field list, which is a segfault rather than a wrong answer.
	own, err := vips.Copy(gray, nil)
	if err != nil {
		return err
	}
	defer own.Close()
	_ = own.Fields()
	own.SetString("soak", "yes")
	_ = own.RemoveField("soak")
	_ = own.Orientation()

	// arrays in and out
	if _, err := vips.Avg(small); err != nil {
		return err
	}
	stats, err := vips.Getpoint(small, 4, 4, nil)
	if err != nil {
		return err
	}
	_ = stats

	// encode through a memory buffer and through a memory target
	if _, err := vips.SaveBuffer(gray, ".png"); err != nil {
		return err
	}
	target, err := vips.NewTargetToMemory()
	if err != nil {
		return err
	}
	defer target.Close()
	if err := vips.WebpsaveTarget(small, target, nil); err != nil {
		return err
	}
	if _, err := target.Bytes(); err != nil {
		return err
	}

	// and a source, so the loader path is covered as well
	src, err := vips.NewSourceFromFile(path)
	if err != nil {
		return err
	}
	defer src.Close()
	viaSource, err := vips.PngloadSource(src, nil)
	if err != nil {
		return err
	}
	viaSource.Close()

	// Reader in, writer out. This one has its own way of leaking: every
	// reader-backed source holds a registry entry on the Go side, and a Close
	// that misses would pin the reader for the life of the process without
	// showing up in any of libvips' counters.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	streamed, err := vips.NewSourceFromReader(f)
	if err != nil {
		return err
	}
	defer streamed.Close()
	fromReader, err := vips.PngloadSource(streamed, nil)
	if err != nil {
		return err
	}
	defer fromReader.Close()

	var sink bytes.Buffer
	writerTarget, err := vips.NewTargetToWriter(&sink)
	if err != nil {
		return err
	}
	defer writerTarget.Close()
	if err := vips.JpegsaveTarget(fromReader, writerTarget, nil); err != nil {
		return err
	}
	if err := writerTarget.Err(); err != nil {
		return err
	}

	// Strip copies and removes, so it exercises the metadata path on a private
	// header rather than a shared one.
	bare, err := fromReader.Strip()
	if err != nil {
		return err
	}
	bare.Close()

	return nil
}

// settle runs the collector and gives cleanups a moment, since a handle's
// reference is released on a cleanup goroutine rather than inline.
// settle runs the collector and drains the operation cache, which legitimately
// retains built operations and the images they reference. Without draining it,
// a warm cache and a leak look identical from out here.
func settle() {
	for range 3 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	vips.ClearCache()
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
}

func TestNoGrowthUnderLoad(t *testing.T) {
	src := fixture(t)

	rounds := 200
	if testing.Short() {
		rounds = 40
	}

	// Warm up first. The first passes allocate thread state, load plugins and
	// grow arenas, none of which is a leak.
	for range 30 {
		if err := pipeline(src); err != nil {
			t.Fatalf("warm-up: %v", err)
		}
	}
	settle()
	before := vips.Memory()
	t.Logf("after warm-up: %d bytes, %d allocs, %d files",
		before.Bytes, before.Allocs, before.Files)

	for i := range rounds {
		if err := pipeline(src); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	settle()
	after := vips.Memory()
	t.Logf("after %d rounds: %d bytes, %d allocs, %d files (peak %d)",
		rounds, after.Bytes, after.Allocs, after.Files, after.HighWater)

	// Each round allocates and frees megabytes. If anything is retained per
	// round the counters climb without bound, so a small fixed allowance
	// separates noise from a leak.
	if growth := int64(after.Bytes) - int64(before.Bytes); growth > 1<<20 {
		t.Errorf("libvips memory grew by %d bytes over %d rounds", growth, rounds)
	}
	if growth := after.Allocs - before.Allocs; growth > 16 {
		t.Errorf("live allocations grew by %d over %d rounds", growth, rounds)
	}
	if after.Files > before.Files {
		t.Errorf("tracked file descriptors grew from %d to %d",
			before.Files, after.Files)
	}
	if n := vips.OpenStreams(); n != 0 {
		t.Errorf("%d reader- or writer-backed streams outlived their Close", n)
	}
}

// The same work from many goroutines at once. Reference counting that is
// correct serially can still be wrong under contention.
func TestNoGrowthConcurrently(t *testing.T) {
	src := fixture(t)

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	perWorker := 40
	if testing.Short() {
		perWorker = 8
	}

	for range 20 {
		if err := pipeline(src); err != nil {
			t.Fatalf("warm-up: %v", err)
		}
	}
	settle()
	before := vips.Memory()

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range perWorker {
				if err := pipeline(src); err != nil {
					errs <- fmt.Errorf("worker %d round %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	settle()
	after := vips.Memory()
	t.Logf("%d workers x %d rounds: %d -> %d bytes, %d -> %d allocs, peak %d",
		workers, perWorker, before.Bytes, after.Bytes,
		before.Allocs, after.Allocs, after.HighWater)

	if growth := int64(after.Bytes) - int64(before.Bytes); growth > 4<<20 {
		t.Errorf("libvips memory grew by %d bytes under concurrency", growth)
	}
	if after.Files > before.Files {
		t.Errorf("tracked file descriptors grew from %d to %d",
			before.Files, after.Files)
	}
}

// Close and the collector must not both release the same reference. This is the
// shape of the double free that showed up early in development: it survived
// short tests and only appeared once the collector ran while handles were still
// being churned.
func TestCloseAndCollectorDoNotBothFree(t *testing.T) {
	src := fixture(t)

	rounds := 400
	if testing.Short() {
		rounds = 60
	}
	for i := range rounds {
		im, err := vips.LoadFile(src)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		out, err := vips.Resize(im, 0.5, nil)
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}

		// Close explicitly, then drop the reference and force collection so any
		// surviving cleanup would fire on freed memory.
		im.Close()
		out.Close()
		im, out = nil, nil
		_ = im
		_ = out

		if i%25 == 0 {
			runtime.GC()
		}
	}
	settle()
	t.Logf("survived %d explicit-close-then-collect rounds", rounds)
}

// Closing twice, and closing something already collected, must be harmless.
func TestDoubleCloseIsSafe(t *testing.T) {
	src := fixture(t)

	for range 50 {
		im, err := vips.LoadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		im.Close()
		im.Close()
		im.Close()
	}

	target, err := vips.NewTargetToMemory()
	if err != nil {
		t.Fatal(err)
	}
	target.Close()
	target.Close()

	source, err := vips.NewSourceFromFile(src)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	source.Close()

	interp, err := vips.NewInterpolate("bilinear")
	if err != nil {
		t.Fatal(err)
	}
	interp.Close()
	interp.Close()

	settle()
}

// Errors must not strand the images an operation had already produced.
func TestErrorPathDoesNotLeak(t *testing.T) {
	src := fixture(t)

	im, err := vips.LoadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()

	settle()
	before := vips.Memory()

	rounds := 500
	if testing.Short() {
		rounds = 50
	}
	for range rounds {
		// wrong argument name
		if _, err := vips.Call("gaussblur", vips.In("in", im), vips.In("nope", 1)); err == nil {
			t.Fatal("expected an error")
		}
		// wrong type
		if _, err := vips.Call("gaussblur", vips.In("in", im), vips.In("sigma", "x")); err == nil {
			t.Fatal("expected an error")
		}
		// rejected by libvips itself
		if _, err := vips.Call("extract_area", vips.In("input", im),
			vips.In("left", 1<<20), vips.In("top", 0),
			vips.In("width", 10), vips.In("height", 10)); err == nil {
			t.Fatal("expected an error")
		}
	}

	settle()
	after := vips.Memory()
	t.Logf("%d error rounds: %d -> %d bytes, %d -> %d allocs",
		rounds, before.Bytes, after.Bytes, before.Allocs, after.Allocs)

	if growth := int64(after.Bytes) - int64(before.Bytes); growth > 1<<20 {
		t.Errorf("failed calls retained %d bytes over %d rounds", growth, rounds)
	}
}

func TestMain(m *testing.M) {
	vips.SetConcurrency(2)
	os.Exit(m.Run())
}
