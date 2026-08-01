package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

// ClosedError is the value panicked with when a closed handle is used.
//
// It is an error rather than a string so a caller who wants to survive the
// mistake can recover and type-assert it. Reaching C with a closed handle used
// to be a SIGSEGV inside libvips, which no amount of recovering helps.
type ClosedError struct {
	Op string // the method that was called
}

func (e *ClosedError) Error() string {
	return "vips: " + e.Op + " called on a closed handle"
}

// Image is a handle on a VipsImage. It carries one reference; the reference is
// dropped by Close, or by the garbage collector if Close is never called.
//
// An Image is a node in a lazily evaluated pipeline, not a buffer of pixels.
// Constructing one is cheap and does no decoding; work happens when pixels are
// finally demanded, typically by a save operation.
//
// The handle is safe to use from several goroutines, Close included. Every
// method takes its own reference on the underlying object before entering C
// and drops it afterwards, so a Close racing a call either loses — the call
// finishes on its own reference and the object is freed when the last
// reference goes — or wins, in which case the call panics with *ClosedError.
// Neither path reads freed memory. What stays the caller's problem is meaning:
// an image is a lazy pipeline, and evaluating one from two goroutines at once
// is not safe; see CopyMemory.
type Image struct {
	// mu makes taking a reference and revoking the handle mutually exclusive.
	// An atomic pointer alone cannot: between loading the pointer and reffing
	// it, a concurrent Close could drop the last reference and free the object.
	mu      sync.Mutex
	ptr     *C.VipsImage // guarded by mu; nil once closed
	cleanup runtime.Cleanup

	// watch is the attached progress handler, if any. It lives here so Close
	// can detach it first: disconnecting a signal from an object that has just
	// been unreffed is a use-after-free, and the ordering should not be the
	// caller's problem.
	watch atomic.Pointer[Watch]
}

// wrapImage takes ownership of one reference to a VipsImage.
func wrapImage(p unsafe.Pointer) *Image {
	if p == nil {
		return nil
	}
	cp := (*C.VipsImage)(p)
	im := &Image{ptr: cp}
	im.cleanup = runtime.AddCleanup(im, func(ptr *C.VipsImage) {
		// There is nothing left to release once libvips has been shut down,
		// and the collector is free to run this afterwards.
		if shutdownDone.Load() {
			return
		}
		C.vipsx_image_unref(ptr)
	}, cp)
	return im
}

// Close drops this image's reference. Further use panics with *ClosedError.
// Calling Close more than once is safe and does nothing after the first call.
//
// The garbage collector holds a cleanup for the same reference, so Close must
// cancel it before unreffing. Without that the collector would unref a second
// time once this handle became unreachable, which is a use-after-free rather
// than a leak.
func (im *Image) Close() {
	if im == nil {
		return
	}
	// Detach before the object goes: the handler holds signal ids on it.
	im.watch.Load().Stop()

	// Taking the pointer under the lock is the claim: exactly one caller gets
	// it, and no acquire can slip between the take and the unref.
	im.mu.Lock()
	p := im.ptr
	im.ptr = nil
	im.mu.Unlock()
	if p == nil {
		return
	}
	im.cleanup.Stop()
	C.vipsx_image_unref(p)
}

// acquire returns the underlying VipsImage with an extra reference held, or
// panics if the handle is closed. Every method that hands the pointer to C
// goes through here and drops the reference with release once C has returned.
//
// The reference is what makes a concurrent Close defined: Close revokes the
// handle, but it cannot free an object a call is still using. Passing a NULL
// along instead of panicking is not an option: libvips reads through it, so a
// use-after-close became a SIGSEGV that took the process with it and named
// neither the image nor the caller.
func (im *Image) acquire(op string) *C.VipsImage {
	p := im.tryAcquire()
	if p == nil {
		panic(&ClosedError{Op: op})
	}
	return p
}

// tryAcquire is acquire without the panic: nil means the handle is closed.
// It feeds argument marshalling, which turns a closed handle into a returned
// error naming the argument.
func (im *Image) tryAcquire() *C.VipsImage {
	if im == nil {
		return nil
	}
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.ptr == nil {
		return nil
	}
	C.vipsx_object_ref(unsafe.Pointer(im.ptr))
	return im.ptr
}

// release drops the reference acquire took.
func (im *Image) release(p *C.VipsImage) {
	C.vipsx_image_unref(p)
}

// acquireObject implements cObject; see marshal.
func (im *Image) acquireObject() unsafe.Pointer {
	return unsafe.Pointer(im.tryAcquire())
}

// Width reports the image width in pixels.
func (im *Image) Width() int {
	p := im.acquire("Width")
	defer im.release(p)
	return int(C.vipsx_image_width(p))
}

// Height reports the image height in pixels.
func (im *Image) Height() int {
	p := im.acquire("Height")
	defer im.release(p)
	return int(C.vipsx_image_height(p))
}

// Bands reports the number of bands per pixel.
func (im *Image) Bands() int {
	p := im.acquire("Bands")
	defer im.release(p)
	return int(C.vipsx_image_bands(p))
}

// cObject is implemented by handles that wrap a GObject-derived pointer.
// acquireObject returns the pointer with an extra reference held, or nil when
// the handle is closed; the caller releases with vipsx_object_unref.
type cObject interface {
	acquireObject() unsafe.Pointer
}
