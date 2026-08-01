package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"runtime"
	"sort"
	"unsafe"
)

// OrientationSwaps reports whether applying the EXIF orientation would exchange
// width and height.
//
// This is the question, not the tag. Orientation 5 through 8 mean the image is
// stored rotated a quarter turn, so Width and Height are the wrong way round
// until Autorot runs — and sizing a thumbnail from them without asking gets it
// wrong on a good fraction of what a phone produces.
//
//	w, h := im.Width(), im.Height()
//	if im.OrientationSwaps() {
//	    w, h = h, w
//	}
func (im *Image) OrientationSwaps() bool {
	p := im.live("OrientationSwaps")
	defer runtime.KeepAlive(im)
	return C.vipsx_image_get_orientation_swap(p) != 0
}

// GuessFormat reports the band format libvips would use for this image if it
// had to choose, which is not always the one it currently carries.
func (im *Image) GuessFormat() int {
	p := im.live("GuessFormat")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_guess_format(p))
}

// GuessInterpretation reports what libvips thinks these pixels mean, inferred
// from band count and format rather than read from the header. Useful when a
// loader left the interpretation unset or wrong.
func (im *Image) GuessInterpretation() int {
	p := im.live("GuessInterpretation")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_guess_interpretation(p))
}

// Filename reports where this image was loaded from, or "" for one that was
// not loaded from a file. Worth putting in an error message.
func (im *Image) Filename() string {
	p := im.live("Filename")
	defer runtime.KeepAlive(im)
	return C.GoString(C.vipsx_image_get_filename(p))
}

// History reports the operations libvips recorded on the way to this image.
//
// libvips keeps its own note of what was applied, which is a better answer to
// "how did this image get like this" than anything reconstructed afterwards.
func (im *Image) History() string {
	p := im.live("History")
	defer runtime.KeepAlive(im)
	return C.GoString(C.vipsx_image_get_history(p))
}

// Minimise releases what the pipeline behind this image is holding open —
// file descriptors, mainly — without ending the image, which reopens whatever
// it needs on the next demand.
//
// For a process holding many images at once, this is the difference between
// running out of descriptors and not.
func (im *Image) Minimise() {
	p := im.live("Minimise")
	defer runtime.KeepAlive(im)
	C.vipsx_image_minimise_all(p)
}

// Invalidate throws away any pixels cached for this image, so the next demand
// recomputes them. This is what to call when the file underneath has changed.
func (im *Image) Invalidate() {
	p := im.live("Invalidate")
	defer runtime.KeepAlive(im)
	C.vipsx_image_invalidate_all(p)
}

// ---------------------------------------------------------------------------
// Band formats.
//
// Predicates rather than a table in Go, which libvips would eventually
// disagree with. Each takes a VipsBandFormat as an int, the same as
// (*Image).Format reports.
// ---------------------------------------------------------------------------

// Is8Bit reports whether one band fits in a byte, which is what makes the
// output of WriteToMemory readable as a plain []byte.
func Is8Bit(format int) bool { return C.vipsx_band_format_is8bit(C.int(format)) != 0 }

// IsInt reports whether the format is an integer one, signed or not.
func IsInt(format int) bool { return C.vipsx_band_format_isint(C.int(format)) != 0 }

// IsUint reports whether the format is an unsigned integer one.
func IsUint(format int) bool { return C.vipsx_band_format_isuint(C.int(format)) != 0 }

// IsFloat reports whether the format is a floating point one.
func IsFloat(format int) bool { return C.vipsx_band_format_isfloat(C.int(format)) != 0 }

// IsComplex reports whether the format holds complex numbers, where one band
// is two numbers.
func IsComplex(format int) bool { return C.vipsx_band_format_iscomplex(C.int(format)) != 0 }

// MaxAlpha reports the value a fully opaque alpha band carries in the given
// interpretation: 255 for 8-bit, 65535 for 16-bit, 1.0 for the float spaces.
//
// Compositing with the wrong one silently produces a transparent image, which
// is a slow thing to debug.
func MaxAlpha(interpretation int) float64 {
	return float64(C.vipsx_interpretation_max_alpha(C.int(interpretation)))
}

// ---------------------------------------------------------------------------
// What the library can do.
// ---------------------------------------------------------------------------

// MaxCoord reports the largest width or height libvips will accept. Nothing
// bigger can be loaded or created, so it is the ceiling any size check sits
// under.
func MaxCoord() int { return int(C.vipsx_max_coord_get()) }

// SaveSuffixes lists every filename extension this libvips can write, such as
// ".jpg" and ".webp", sorted and without repeats.
//
// Which formats a build supports depends on how it was compiled, so a service
// that accepts an output format from a request should check against this
// rather than against a list written down somewhere.
//
// libvips reports a suffix once per saver that claims it, so ".mat" arrives
// three times. Nothing is carried by the repetition — no order, no ownership —
// so it is dropped here rather than left for every caller to handle.
func SaveSuffixes() []string {
	if err := Startup(); err != nil {
		return nil
	}
	arr := C.vipsx_foreign_get_suffixes()
	if arr == nil {
		return nil
	}
	defer C.vipsx_strv_gfree(arr)

	seen := map[string]bool{}
	var out []string
	for p := arr; *p != nil; p = (**C.char)(unsafe.Add(unsafe.Pointer(p), unsafe.Sizeof(*p))) {
		if s := C.GoString(*p); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// LoadFlags describes how a loader will read a particular file.
type LoadFlags struct {
	// Partial is true when the file can be read a region at a time, so a crop
	// costs the crop rather than the whole image.
	Partial bool
	// BigEndian is true when the pixels are stored most significant byte first.
	BigEndian bool
	// Sequential is true when the file must be read top to bottom, which rules
	// out anything that wants to jump around it.
	Sequential bool
}

// LoaderFlags reports how the named loader will read the named file. The loader
// is a nickname as LoaderFor reports it, such as "jpegload".
func LoaderFlags(loader, filename string) (LoadFlags, error) {
	if err := Startup(); err != nil {
		return LoadFlags{}, err
	}
	cloader := C.CString(loader)
	defer C.free(unsafe.Pointer(cloader))
	cname := C.CString(filename)
	defer C.free(unsafe.Pointer(cname))

	f := int(C.vipsx_foreign_flags(cloader, cname))
	return LoadFlags{
		Partial:    f&1 != 0,
		BigEndian:  f&2 != 0,
		Sequential: f&4 != 0,
	}, nil
}

// SplitFilename separates libvips' own filename-with-options syntax, where
// "photo.jpg[Q=90,strip]" names a file and the arguments to load it with.
//
// The syntax turns up in anything that came from the command line or a config
// file, and treating the whole string as a path fails in a way that reads like
// a missing file.
//
// options keeps the brackets libvips puts round it, so path + options is the
// input again. Handed over as libvips renders it rather than tidied, for the
// same reason the EXIF values are: whoever needs to parse it knows what they
// are parsing, and this way nothing is lost on the way through.
//
//	path, options := vips.SplitFilename("photo.jpg[Q=90]")
//	// path == "photo.jpg", options == "[Q=90]"
func SplitFilename(name string) (path, options string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cpath := C.vipsx_filename_get_filename(cname)
	if cpath != nil {
		path = C.GoString(cpath)
		C.vipsx_gfree(unsafe.Pointer(cpath))
	}
	copts := C.vipsx_filename_get_options(cname)
	if copts != nil {
		options = C.GoString(copts)
		C.vipsx_gfree(unsafe.Pointer(copts))
	}
	return path, options
}

// FreezeErrors stops libvips appending to its error buffer until ThawErrors.
//
// For work that is expected to fail — probing several loaders to see which
// takes a file, say — where the failures are not the caller's business and
// would otherwise pile up in a buffer shared by the whole process. Calls nest:
// each Freeze needs its Thaw.
//
//	vips.FreezeErrors()
//	defer vips.ThawErrors()
func FreezeErrors() { C.vipsx_error_freeze() }

// ThawErrors resumes error collection after FreezeErrors.
func ThawErrors() { C.vipsx_error_thaw() }
