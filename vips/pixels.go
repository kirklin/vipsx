package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"math"
	"math/bits"
	"runtime"
	"unsafe"
)

// pixelBytes multiplies image dimensions without wrapping. The dimensions are
// the caller's to choose, and a product that overflowed into something small
// would pass the length check and send libvips reading past the buffer.
func pixelBytes(width, height, bands, sizeof int) (int, bool) {
	if sizeof <= 0 {
		return 0, false
	}
	hi, n := bits.Mul64(uint64(width), uint64(height))
	if hi != 0 {
		return 0, false
	}
	hi, n = bits.Mul64(n, uint64(bands))
	if hi != 0 {
		return 0, false
	}
	hi, n = bits.Mul64(n, uint64(sizeof))
	if hi != 0 || n > math.MaxInt {
		return 0, false
	}
	return int(n), true
}

// FormatSizeof reports how many bytes one band of one pixel takes in the given
// band format. Multiply by bands and by width×height for a raw buffer's size.
//
// The format is a VipsBandFormat as an int, which is what (*Image).Format
// reports and what the generated BandFormat constants are: pass im.Format()
// straight through, or int(vips.BandFormatUchar) for a literal. This layer
// takes the int because the generator imports it, and a hand-written file that
// referred to a generated type could not be compiled before the generated
// files existed.
func FormatSizeof(format int) int {
	return int(C.vipsx_format_sizeof(C.int(format)))
}

// NewImageFromMemory wraps raw pixel data as an image.
//
// The data is band-packed by row: for an 8-bit RGB image, three bytes per pixel
// with no padding, width×height×3 in all. FormatSizeof gives the size of one
// band for other formats.
//
// This is the way in for pixels that did not come from a file — another
// library's decoder, a framebuffer, a Go image.Image. Nothing else in this
// package can start from pixels: the operation layer has rawload, but it only
// reads a filename, so the alternative was writing a temporary file.
//
// libvips takes its own copy, so the slice can be reused or collected as soon
// as this returns. The non-copying constructor exists in libvips and is
// deliberately not exposed: it keeps the caller's pointer for the life of the
// image, and Go memory belongs to a collector that is free to move or reclaim
// it. The copy is one memcpy against the cost of an image.
//
// format is a VipsBandFormat as an int; see FormatSizeof. The round trip needs
// no conversion at all:
//
//	raw, _ := im.WriteToMemory()
//	same, _ := vips.NewImageFromMemory(raw, im.Width(), im.Height(), im.Bands(), im.Format())
func NewImageFromMemory(data []byte, width, height, bands int, format int) (*Image, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 || bands <= 0 {
		return nil, &Error{
			Op:      "new_from_memory",
			Message: fmt.Sprintf("dimensions must be positive, got %dx%d with %d bands", width, height, bands),
		}
	}

	want, ok := pixelBytes(width, height, bands, FormatSizeof(format))
	if !ok {
		return nil, &Error{
			Op: "new_from_memory",
			Message: fmt.Sprintf("%dx%d with %d bands of format %d does not fit in memory",
				width, height, bands, format),
		}
	}
	if len(data) < want {
		return nil, &Error{
			Op: "new_from_memory",
			Message: fmt.Sprintf("buffer is %d bytes, need %d for %dx%d with %d bands of format %d",
				len(data), want, width, height, bands, format),
		}
	}

	p := C.vipsx_image_new_from_memory(unsafe.Pointer(&data[0]), C.size_t(len(data)),
		C.int(width), C.int(height), C.int(bands), C.int(format))
	runtime.KeepAlive(data)
	if p == nil {
		return nil, &Error{Op: "new_from_memory", Message: lastError()}
	}
	return wrapImage(unsafe.Pointer(p)), nil
}

// WriteToMemory renders the pipeline and returns the pixels, band-packed by row
// in the image's own band format.
//
// This is the way out for pixels that are not going into a file. It is not an
// encode: there is no JPEG header on the result, only pixels. Use SaveBuffer
// for a file format.
//
// The whole image is computed and held at once, so the answer is as large as
// width×height×bands×FormatSizeof — check before calling it on something big.
func (im *Image) WriteToMemory() ([]byte, error) {
	p := im.acquire("WriteToMemory")
	defer im.release(p)

	var size C.size_t
	buf := C.vipsx_image_write_to_memory(p, &size)
	if buf == nil {
		return nil, &Error{Op: "write_to_memory", Message: lastError()}
	}
	defer C.vipsx_gfree(buf)
	return goBytes(buf, size), nil
}

// CopyMemory renders the pipeline into memory and returns the result as a new
// image. The caller owns it and should Close it.
//
// An Image is normally a promise rather than pixels, and the promise depends on
// whatever it was built from. That has two consequences this answers:
//
// A source cannot be closed before its image is used. Close on a reader-backed
// source releases the reader immediately, so an image still waiting to be
// evaluated fails with a read error. Copying to memory first cuts the tie, and
// the reader can go back to whoever owns it — which in a request handler is the
// difference between holding a connection open until the save finishes and not:
//
//	src, _ := vips.NewSourceFromReader(req.Body)
//	im, _ := vips.JpegloadSource(src, nil)
//	own, _ := im.CopyMemory()   // pixels are here now
//	src.Close()                 // and the body can go back
//
// A lazy image is also not safe to evaluate from two goroutines at once, while
// a memory image is. Copying is what makes an image shareable.
//
// libvips refs and returns the same image when it is already a memory area, so
// calling this on something already materialised costs nothing. That economy
// is also the caveat: the "copy" can be the same object, so this is not a way
// to get a private image to mutate. Metadata written through such an alias
// lands on the original — and on every other holder, since identical calls
// served from the operation cache share their result. To mutate, take a real
// copy first: Copy from the generated layer, or Call("copy") from this one.
func (im *Image) CopyMemory() (*Image, error) {
	p := im.acquire("CopyMemory")
	defer im.release(p)

	out := C.vipsx_image_copy_memory(p)
	if out == nil {
		return nil, &Error{Op: "copy_memory", Message: lastError()}
	}
	return wrapImage(unsafe.Pointer(out)), nil
}

// WithPixels lends the image's own pixel buffer to fn, without copying it.
//
// WriteToMemory allocates a copy and hands it over; this renders into the
// image's buffer and lets fn look at it in place. On a large image that is a
// whole memcpy saved, which is the only reason to prefer it.
//
// The slice belongs to the image and is valid only until fn returns. It must
// not escape: keeping it, appending it to something outlasting the call, or
// handing it to a goroutine that outlives fn all leave a slice pointing into
// memory the image may have released. Copy what is needed instead.
//
//	var sum uint64
//	err := im.WithPixels(func(p []byte) error {
//	    for _, b := range p {
//	        sum += uint64(b)
//	    }
//	    return nil
//	})
//
// The callback shape is the API this deserves rather than the API libvips has.
// vips_image_get_data returns the pointer, and a Go function returning a []byte
// over it would be handing out a use-after-free waiting for someone to Close
// the image.
func (im *Image) WithPixels(fn func([]byte) error) error {
	if fn == nil {
		return &Error{Op: "get_data", Message: "nil callback"}
	}
	p := im.acquire("WithPixels")
	defer im.release(p)

	data := C.vipsx_image_get_data(p)
	if data == nil {
		return &Error{Op: "get_data", Message: lastError()}
	}

	n := im.Width() * im.Height() * im.Bands() * FormatSizeof(im.Format())
	if n <= 0 {
		return &Error{Op: "get_data", Message: "the image has no pixels"}
	}
	// unsafe.Slice over C memory: the image holds it, and KeepAlive above holds
	// the image for the length of the call.
	return fn(unsafe.Slice((*byte)(data), n))
}
