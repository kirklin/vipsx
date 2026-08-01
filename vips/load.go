package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"runtime"
	"unsafe"
)

// LoaderFor reports which operation can read the given file, for example
// "jpegload" or "pngload". libvips decides by sniffing content, not by
// trusting the extension.
//
// The name is the operation nickname, the same spelling Operations and Describe
// use, so it can be compared against them and passed to Call. libvips answers
// this lookup with a class name instead — VipsForeignLoadHeifFile rather than
// heifload — and both work when calling, but only one of them matches anything
// else in this package.
func LoaderFor(path string) (string, error) {
	if err := Startup(); err != nil {
		return "", err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	name := C.vipsx_find_load(cpath)
	if name == nil {
		return "", &Error{Op: "find_load", Message: lastError()}
	}
	return C.GoString(C.vipsx_nickname_for(name)), nil
}

// LoaderForBuffer reports which operation can read the given bytes.
func LoaderForBuffer(buf []byte) (string, error) {
	if err := Startup(); err != nil {
		return "", err
	}
	if len(buf) == 0 {
		return "", &Error{Op: "find_load_buffer", Message: "empty buffer"}
	}
	name := C.vipsx_find_load_buffer(unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
	if name == nil {
		return "", &Error{Op: "find_load_buffer", Message: lastError()}
	}
	return C.GoString(C.vipsx_nickname_for(name)), nil
}

// SaverFor reports which operation writes the given filename, chosen by its
// extension, for example "webpsave".
func SaverFor(path string) (string, error) {
	if err := Startup(); err != nil {
		return "", err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	name := C.vipsx_find_save(cpath)
	if name == nil {
		return "", &Error{Op: "find_save", Message: lastError()}
	}
	return C.GoString(C.vipsx_nickname_for(name)), nil
}

// SaverForBuffer reports which operation writes the given format to memory.
// The suffix is an extension such as ".jpg".
func SaverForBuffer(suffix string) (string, error) {
	if err := Startup(); err != nil {
		return "", err
	}
	csuffix := C.CString(suffix)
	defer C.free(unsafe.Pointer(csuffix))

	name := C.vipsx_find_save_buffer(csuffix)
	if name == nil {
		return "", &Error{Op: "find_save_buffer", Message: lastError()}
	}
	return C.GoString(C.vipsx_nickname_for(name)), nil
}

// LoaderForSource reports which operation can read the given source, sniffing
// its content the way LoaderFor does for a file.
//
// Sniffing rewinds the source afterwards, so the loader that runs next still
// reads from the start. A source that cannot seek is buffered by libvips for
// exactly this reason.
func LoaderForSource(src *Source) (string, error) {
	if err := Startup(); err != nil {
		return "", err
	}
	p := src.live("LoaderForSource")
	defer runtime.KeepAlive(src)

	name := C.vipsx_find_load_source((*C.VipsSource)(p))
	if name == nil {
		return "", &Error{Op: "find_load_source", Message: lastError()}
	}
	return C.GoString(C.vipsx_nickname_for(name)), nil
}

// SaverForTarget reports which operation writes the given format to a target.
// The suffix is an extension such as ".webp".
func SaverForTarget(suffix string) (string, error) {
	if err := Startup(); err != nil {
		return "", err
	}
	csuffix := C.CString(suffix)
	defer C.free(unsafe.Pointer(csuffix))

	name := C.vipsx_find_save_target(csuffix)
	if name == nil {
		return "", &Error{Op: "find_save_target", Message: lastError()}
	}
	return C.GoString(C.vipsx_nickname_for(name)), nil
}

// LoadFile reads an image, picking the loader from the file's content. Extra
// arguments are passed to that loader, so loader-specific options work here:
//
//	im, err := vips.LoadFile("photo.jpg", vips.In("shrink", 2))
func LoadFile(path string, args ...Arg) (*Image, error) {
	loader, err := LoaderFor(path)
	if err != nil {
		return nil, err
	}
	outs, err := Call(loader, append([]Arg{In("filename", path)}, args...)...)
	if err != nil {
		return nil, err
	}
	return outs.Image("out")
}

// LoadBuffer reads an image from memory, picking the loader from its content.
func LoadBuffer(buf []byte, args ...Arg) (*Image, error) {
	loader, err := LoaderForBuffer(buf)
	if err != nil {
		return nil, err
	}
	outs, err := Call(loader, append([]Arg{In("buffer", buf)}, args...)...)
	if err != nil {
		return nil, err
	}
	return outs.Image("out")
}

// SaveFile writes an image, picking the saver from the filename's extension.
func SaveFile(im *Image, path string, args ...Arg) error {
	saver, err := SaverFor(path)
	if err != nil {
		return err
	}
	outs, err := Call(saver, append([]Arg{In("in", im), In("filename", path)}, args...)...)
	if err != nil {
		return err
	}
	outs.Close()
	return nil
}

// SaveBuffer encodes an image to memory. The suffix selects the format, for
// example ".webp".
func SaveBuffer(im *Image, suffix string, args ...Arg) ([]byte, error) {
	saver, err := SaverForBuffer(suffix)
	if err != nil {
		return nil, err
	}
	outs, err := Call(saver, append([]Arg{In("in", im)}, args...)...)
	if err != nil {
		return nil, err
	}
	defer outs.Close()
	return outs.Bytes("buffer")
}

// LoadSource reads an image from a source, picking the loader from its content.
//
// This is the streaming counterpart of LoadFile, and it closes a hole rather
// than adding a convenience: a file or a buffer could be sniffed, a source
// could not, so streaming was the one path where the caller had to know the
// format and name the loader — vips.JpegloadSource and nothing else. An HTTP
// body is exactly where that knowledge is least available.
//
//	src, _ := vips.NewSourceFromReader(req.Body)
//	defer src.Close()
//	im, err := vips.LoadSource(src)
func LoadSource(src *Source, args ...Arg) (*Image, error) {
	loader, err := LoaderForSource(src)
	if err != nil {
		return nil, err
	}
	outs, err := Call(loader, append([]Arg{In("source", src)}, args...)...)
	if err != nil {
		return nil, err
	}
	return outs.Image("out")
}

// SaveTarget writes an image to a target. The suffix selects the format, for
// example ".webp".
//
//	tg, _ := vips.NewTargetToWriter(w)
//	defer tg.Close()
//	err := vips.SaveTarget(im, tg, ".webp", vips.In("Q", 82))
func SaveTarget(im *Image, tg *Target, suffix string, args ...Arg) error {
	saver, err := SaverForTarget(suffix)
	if err != nil {
		return err
	}
	outs, err := Call(saver, append([]Arg{In("in", im), In("target", tg)}, args...)...)
	if err != nil {
		return err
	}
	outs.Close()
	return nil
}
