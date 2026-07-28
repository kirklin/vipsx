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

// Image is a handle on a VipsImage. It carries one reference; the reference is
// dropped by Close, or by the garbage collector if Close is never called.
//
// An Image is a node in a lazily evaluated pipeline, not a buffer of pixels.
// Constructing one is cheap and does no decoding; work happens when pixels are
// finally demanded, typically by a save operation.
type Image struct {
	ptr     unsafe.Pointer // *C.VipsImage
	cleanup runtime.Cleanup
	closed  atomic.Bool
}

// wrapImage takes ownership of one reference to a VipsImage.
func wrapImage(p unsafe.Pointer) *Image {
	if p == nil {
		return nil
	}
	im := &Image{ptr: p}
	im.cleanup = runtime.AddCleanup(im, func(ptr unsafe.Pointer) {
		C.vipsx_image_unref((*C.VipsImage)(ptr))
	}, p)
	return im
}

// Close drops this image's reference. Further use is invalid. Calling Close
// more than once is safe and does nothing after the first call.
//
// The garbage collector holds a cleanup for the same reference, so Close must
// cancel it before unreffing. Without that the collector would unref a second
// time once this handle became unreachable, which is a use-after-free rather
// than a leak.
func (im *Image) Close() {
	if im == nil || !im.closed.CompareAndSwap(false, true) {
		return
	}
	im.cleanup.Stop()
	C.vipsx_image_unref((*C.VipsImage)(im.ptr))
	im.ptr = nil
	runtime.KeepAlive(im)
}

func (im *Image) cPointer() unsafe.Pointer { return im.ptr }

// Width reports the image width in pixels.
func (im *Image) Width() int {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_width((*C.VipsImage)(im.ptr)))
}

// Height reports the image height in pixels.
func (im *Image) Height() int {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_height((*C.VipsImage)(im.ptr)))
}

// Bands reports the number of bands per pixel.
func (im *Image) Bands() int {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_bands((*C.VipsImage)(im.ptr)))
}

// cObject is implemented by handles that wrap a GObject-derived pointer.
type cObject interface {
	cPointer() unsafe.Pointer
}
