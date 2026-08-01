package vips_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kirklin/vipsx/vips"
)

func TestOnProgressReports(t *testing.T) {
	im := black(t, 2000, 2000)
	defer im.Close()

	var (
		ticks atomic.Int64
		last  atomic.Pointer[vips.Progress]
	)
	w, err := im.OnProgress(func(p vips.Progress) error {
		ticks.Add(1)
		last.Store(&p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	blur, err := vips.Gaussblur(im, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blur.Close()
	if _, err := vips.Avg(blur); err != nil {
		t.Fatal(err)
	}

	if ticks.Load() == 0 {
		t.Fatal("evaluation completed without a single progress report")
	}
	p := last.Load()
	if p == nil {
		t.Fatal("no progress recorded")
	}
	if p.Total != 2000*2000 {
		t.Errorf("Total is %d, want %d", p.Total, 2000*2000)
	}
	if p.Done <= 0 || p.Done > p.Total {
		t.Errorf("Done is %d, outside 1..%d", p.Done, p.Total)
	}
	if p.Percent < 0 || p.Percent > 100 {
		t.Errorf("Percent is %d, outside 0..100", p.Percent)
	}
	if w.Err() != nil {
		t.Errorf("a watch that never returned an error reports %v", w.Err())
	}
}

// The reason this exists: libvips reports a killed pipeline as a generic
// operation failure, so the deadline has to be recoverable from somewhere.
//
// Sizing matters here. Measured on a 16-thread machine this pipeline takes
// about 600 ms, against a 60 ms deadline — an order of magnitude, so the test
// is not a race between finishing and being stopped. A slower machine only
// widens the margin. Every evaluation test uses its own dimensions, because
// libvips caches built operations and a second test asking for the same
// pipeline gets the first one's answer instantly.
func TestCancelOnDeadlineStopsEvaluation(t *testing.T) {
	vips.ClearCache()

	im := black(t, 20000, 20000)
	defer im.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	w, err := im.CancelOn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	blur, err := vips.Gaussblur(im, 40, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blur.Close()

	start := time.Now()
	if _, err := vips.Avg(blur); err == nil {
		t.Fatal("evaluation finished despite the deadline")
	}
	elapsed := time.Since(start)

	if !errors.Is(w.Err(), context.DeadlineExceeded) {
		t.Fatalf("Watch.Err() is %v, want context.DeadlineExceeded", w.Err())
	}
	// Prompt rather than immediate: the kill is noticed at the next report.
	// A generous bound, because what is being tested is that it is bounded.
	if elapsed > 10*time.Second {
		t.Errorf("took %v to notice the deadline", elapsed)
	}
	t.Logf("deadline noticed after %v", elapsed.Round(time.Millisecond))
}

func TestCancelOnAlreadyCancelledContext(t *testing.T) {
	im := black(t, 1000, 1000)
	defer im.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w, err := im.CancelOn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if !errors.Is(w.Err(), context.Canceled) {
		t.Fatalf("Watch.Err() is %v, want context.Canceled", w.Err())
	}
	if _, err := vips.Avg(im); err == nil {
		t.Fatal("evaluation ran under a context that was already done")
	}
}

// An error from the callback is the general form; a context is one use of it.
func TestProgressCallbackErrorStopsEvaluation(t *testing.T) {
	vips.ClearCache()

	im := black(t, 8000, 8000)
	defer im.Close()

	sentinel := errors.New("enough of that")
	w, err := im.OnProgress(func(p vips.Progress) error {
		if p.Percent > 5 {
			return sentinel
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	blur, err := vips.Gaussblur(im, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blur.Close()

	if _, err := vips.Avg(blur); err == nil {
		t.Fatal("evaluation finished although the callback asked to stop")
	}
	if !errors.Is(w.Err(), sentinel) {
		t.Fatalf("Watch.Err() is %v, want the callback's error", w.Err())
	}
}

func TestOnlyOneWatchPerImage(t *testing.T) {
	im := black(t, 64, 64)
	defer im.Close()

	w, err := im.OnProgress(func(vips.Progress) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if _, err := im.OnProgress(func(vips.Progress) error { return nil }); err == nil {
		t.Fatal("a second watch was attached silently")
	}

	// Stopping the first frees the slot.
	w.Stop()
	w2, err := im.OnProgress(func(vips.Progress) error { return nil })
	if err != nil {
		t.Fatalf("could not attach after stopping the first watch: %v", err)
	}
	w2.Stop()
}

func TestWatchStopIsIdempotent(t *testing.T) {
	im := black(t, 64, 64)
	defer im.Close()

	w, err := im.OnProgress(func(vips.Progress) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	w.Stop()
	w.Stop()
	w.Stop()
}

// Closing an image with a watch still attached has to detach it first:
// disconnecting a signal from an object that has just been unreffed is a
// use-after-free, and the ordering should not be the caller's problem.
func TestCloseDetachesAnAttachedWatch(t *testing.T) {
	for i := 0; i < 50; i++ {
		im := black(t, 64, 64)
		w, err := im.OnProgress(func(vips.Progress) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		im.Close()
		w.Stop() // must be safe in this order too
	}
}

// Kill is the primitive under CancelOn, and works without a watch attached:
// the flag is checked by the pipeline itself, not by the progress machinery.
// Distinct dimensions again, so the operation cache cannot answer with the
// previous test's result.
func TestKillStopsEvaluationFromAnotherGoroutine(t *testing.T) {
	vips.ClearCache()

	im := black(t, 19000, 19000)
	defer im.Close()

	blur, err := vips.Gaussblur(im, 40, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blur.Close()

	go func() {
		time.Sleep(40 * time.Millisecond)
		im.Kill()
	}()

	if _, err := vips.Avg(blur); err == nil {
		t.Fatal("evaluation finished although it was killed")
	}
}
