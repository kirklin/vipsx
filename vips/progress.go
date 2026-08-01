package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Progress is one report from an evaluation in flight.
//
// The numbers are libvips' own. Percent and ETA are estimates it revises as it
// goes, and on a pipeline whose cost is uneven they can move unevenly.
type Progress struct {
	Run     time.Duration // wall clock since evaluation started
	ETA     time.Duration // libvips' estimate of what is left
	Total   int64         // pixels the evaluation expects to compute
	Done    int64         // pixels computed so far
	Percent int           // 0 to 100, libvips' own rounding
}

// Watch is an attached progress handler. Stop detaches it; Err reports why the
// evaluation was stopped, when it was.
type Watch struct {
	id      uint64
	im      *Image // held so the handle cannot be collected while attached
	evalID  C.gulong
	postID  C.gulong
	fn      func(Progress) error
	stopped atomic.Bool

	mu       sync.Mutex
	err      error         // the first error the callback returned
	done     chan struct{} // closed when an evaluation finishes
	doneOnce sync.Once     // posteval fires once per evaluation, not once ever
}

// Err reports the error the callback returned, if it returned one.
//
// This is the question a caller actually has after a killed evaluation.
// libvips reports the kill as a generic operation failure — it knows the
// pipeline was stopped, not that a deadline passed — so the reason lives here.
// It keeps answering after Stop.
func (w *Watch) Err() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *Watch) setErr(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

// Done is closed when a watched evaluation finishes, killed or not. An image
// can be evaluated more than once; this reports the first.
func (w *Watch) Done() <-chan struct{} { return w.done }

// Stop detaches the handler. Calling it more than once is safe.
//
// Stopping is not required for correctness — closing the image does it too —
// but a watch left attached keeps its image alive and keeps calling back.
func (w *Watch) Stop() {
	if w == nil || !w.stopped.CompareAndSwap(false, true) {
		return
	}
	unregisterWatch(w.id)
	w.im.watch.CompareAndSwap(w, nil)

	// A closed image has already been unreffed, and its handlers went with it.
	// The reference taken here is what makes racing a Close safe: either the
	// acquire loses and there is nothing to disconnect, or it wins and the
	// object stays alive through the disconnect.
	if p := w.im.tryAcquire(); p != nil {
		C.vipsx_unwatch_eval(p, w.evalID, w.postID)
		w.im.release(p)
	}
}

var (
	watchMu  sync.RWMutex
	watches  = map[uint64]*Watch{}
	watchSeq atomic.Uint64
)

func lookupWatch(id uint64) *Watch {
	watchMu.RLock()
	defer watchMu.RUnlock()
	return watches[id]
}

func unregisterWatch(id uint64) {
	watchMu.Lock()
	delete(watches, id)
	watchMu.Unlock()
}

// OnProgress reports on evaluation of this image and everything downstream of
// it, and lets the callback stop it.
//
// Returning an error from fn kills the evaluation: the operation that demanded
// the pixels fails, and Err on the returned Watch reports the error that caused
// it. Returning nil carries on.
//
// The callback runs on a libvips worker thread, possibly several, so it has to
// be safe to call concurrently and should be cheap — it sits in the middle of
// the pipeline. It must not call back into this package: with SetErrorIsolation
// on, a Call made from inside the callback waits for the very lock the
// evaluation's own Call is holding while it waits for the callback.
//
// One watch at a time per image; attaching a second is an error rather than a
// silent replacement.
func (im *Image) OnProgress(fn func(Progress) error) (*Watch, error) {
	p := im.acquire("OnProgress")
	defer im.release(p)
	if fn == nil {
		return nil, &Error{Op: "progress", Message: "nil callback"}
	}

	w := &Watch{
		id:   watchSeq.Add(1),
		im:   im,
		fn:   fn,
		done: make(chan struct{}),
	}
	if !im.watch.CompareAndSwap(nil, w) {
		return nil, &Error{Op: "progress", Message: "this image is already being watched"}
	}

	watchMu.Lock()
	watches[w.id] = w
	watchMu.Unlock()

	w.evalID = C.vipsx_watch_eval(p, C.guint64(w.id), &w.postID)
	return w, nil
}

// CancelOn stops evaluation of this image once ctx is done.
//
// This is the timeout an image service needs. A malformed or simply enormous
// image can occupy a worker for as long as it takes, and libvips has no notion
// of a deadline; what it has is a kill flag checked as the pipeline runs. This
// ties the two together.
//
//	w, err := im.CancelOn(ctx)
//	defer w.Stop()
//
//	buf, err := vips.SaveBuffer(im, ".webp")
//	if err != nil {
//	    if cause := w.Err(); cause != nil {
//	        return cause // context.DeadlineExceeded, not "vips: killed"
//	    }
//	    return err
//	}
//
// Cancellation is noticed at the next progress report, so it is prompt rather
// than immediate, and an operation that computes nothing along the way cannot
// be interrupted at all. In practice libvips reports often enough that a
// deadline lands within a fraction of a second; a single decode of one huge
// frame is the case where it does not. A result served from the operation
// cache is the other gap: nothing is evaluated, so there is nothing to cancel
// — and nothing that needs it, since a cached result returns immediately.
func (im *Image) CancelOn(ctx context.Context) (*Watch, error) {
	if ctx == nil {
		return nil, &Error{Op: "progress", Message: "nil context"}
	}
	w, err := im.OnProgress(func(Progress) error { return ctx.Err() })
	if err != nil {
		return nil, err
	}
	// Already over before it began: kill now rather than waiting for a report
	// that would arrive only after the work had been done.
	if err := ctx.Err(); err != nil {
		w.setErr(err)
		im.Kill()
	}
	return w, nil
}

// Kill stops evaluation of this image and everything downstream of it.
//
// The operation waiting on those pixels fails. Unlike Close this does not
// release anything: the handle stays usable. The flag is consumed by the
// pipeline that acts on it, and libvips also resets it when a new evaluation
// starts — so a Kill before any evaluation is running does not carry forward,
// which is why CancelOn works through the progress callback rather than
// relying on this.
//
// The flag lives on the underlying image, which identical cached calls share:
// killing an image another holder also reached through the cache can fail
// that holder's next use of it. Kill what is privately yours — an image mid-
// evaluation in your own pipeline — rather than something just loaded.
func (im *Image) Kill() {
	p := im.acquire("Kill")
	defer im.release(p)
	C.vipsx_image_set_kill(p, 1)
}

// Killed reports whether evaluation of this image has been stopped.
//
// Asking does not disarm: libvips' own read of the flag consumes it, so this
// wrapper puts it back — a getter that changed what it measures would make
// Kill-then-Killed report false with the kill still wanted.
func (im *Image) Killed() bool {
	p := im.acquire("Killed")
	defer im.release(p)
	return C.vipsx_image_iskilled(p) != 0
}

//export vipsxGoEval
func vipsxGoEval(id C.guint64, run, eta C.int, tpels, npels C.gint64, percent C.int) C.int {
	w := lookupWatch(uint64(id))
	if w == nil {
		return 0
	}
	err := w.fn(Progress{
		Run:     time.Duration(run) * time.Second,
		ETA:     time.Duration(eta) * time.Second,
		Total:   int64(tpels),
		Done:    int64(npels),
		Percent: int(percent),
	})
	if err == nil {
		return 0
	}
	w.setErr(err)
	return 1
}

//export vipsxGoEvalDone
func vipsxGoEvalDone(id C.guint64) {
	w := lookupWatch(uint64(id))
	if w == nil {
		return
	}
	w.doneOnce.Do(func() { close(w.done) })
}
