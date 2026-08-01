package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"runtime"
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
// The handle itself is safe to use from several goroutines: the pointer is read
// and cleared atomically, so a Close racing a read cannot produce a torn value.
// That makes the race well defined rather than harmless — a read that loses
// panics with *ClosedError instead of reading freed memory.
type Image struct {
	ptr     atomic.Pointer[C.VipsImage]
	cleanup runtime.Cleanup
}

// wrapImage takes ownership of one reference to a VipsImage.
func wrapImage(p unsafe.Pointer) *Image {
	if p == nil {
		return nil
	}
	cp := (*C.VipsImage)(p)
	im := &Image{}
	im.ptr.Store(cp)
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
	// Swap is the claim: exactly one caller can take the pointer away, so it
	// doubles as the once-only guard the separate flag used to provide.
	p := im.ptr.Swap(nil)
	if p == nil {
		return
	}
	im.cleanup.Stop()
	C.vipsx_image_unref(p)
	runtime.KeepAlive(im)
}

// live returns the underlying VipsImage, or panics if the handle is closed.
//
// Every method that hands the pointer to C goes through here. Passing the NULL
// along instead is not an option: libvips reads through it, so a use-after-close
// became a SIGSEGV that took the process with it and named neither the image
// nor the caller.
func (im *Image) live(op string) *C.VipsImage {
	if im == nil {
		panic(&ClosedError{Op: op})
	}
	p := im.ptr.Load()
	if p == nil {
		panic(&ClosedError{Op: op})
	}
	return p
}

// cPointer reports the underlying pointer, or nil when the handle is closed.
//
// Unlike live this does not panic: it feeds argument marshalling, which turns a
// closed handle into a returned error naming the argument.
func (im *Image) cPointer() unsafe.Pointer {
	if im == nil {
		return nil
	}
	return unsafe.Pointer(im.ptr.Load())
}

// Width reports the image width in pixels.
func (im *Image) Width() int {
	p := im.live("Width")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_width(p))
}

// Height reports the image height in pixels.
func (im *Image) Height() int {
	p := im.live("Height")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_height(p))
}

// Bands reports the number of bands per pixel.
func (im *Image) Bands() int {
	p := im.live("Bands")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_bands(p))
}

// cObject is implemented by handles that wrap a GObject-derived pointer.
type cObject interface {
	cPointer() unsafe.Pointer
}
