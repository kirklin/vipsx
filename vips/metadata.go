package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

// Format reports the numeric band format, matching VipsBandFormat.
func (im *Image) Format() int {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_format((*C.VipsImage)(im.ptr)))
}

// Interpretation reports the colour interpretation, matching
// VipsInterpretation.
func (im *Image) Interpretation() int {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_interpretation((*C.VipsImage)(im.ptr)))
}

// Coding reports the pixel coding, matching VipsCoding.
func (im *Image) Coding() int {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_coding((*C.VipsImage)(im.ptr)))
}

// Resolution reports pixels per millimetre horizontally and vertically.
func (im *Image) Resolution() (x, y float64) {
	defer runtime.KeepAlive(im)
	return float64(C.vipsx_image_xres((*C.VipsImage)(im.ptr))),
		float64(C.vipsx_image_yres((*C.VipsImage)(im.ptr)))
}

// Offset reports the image origin, which some operations set.
func (im *Image) Offset() (x, y int) {
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_xoffset((*C.VipsImage)(im.ptr))),
		int(C.vipsx_image_yoffset((*C.VipsImage)(im.ptr)))
}

// HasAlpha reports whether the last band is an alpha channel.
func (im *Image) HasAlpha() bool {
	defer runtime.KeepAlive(im)
	return C.vipsx_image_has_alpha((*C.VipsImage)(im.ptr)) != 0
}

// Fields lists the metadata field names carried on the image, including the
// EXIF, XMP and ICC entries a loader attached.
func (im *Image) Fields() []string {
	defer runtime.KeepAlive(im)
	var count C.int
	cfields := C.vipsx_image_fields((*C.VipsImage)(im.ptr), &count)
	if cfields == nil {
		return nil
	}
	defer C.vipsx_strv_gfree(cfields)

	out := make([]string, 0, int(count))
	for _, c := range unsafe.Slice(cfields, int(count)) {
		out = append(out, C.GoString(c))
	}
	return out
}

// HasField reports whether a metadata field is present.
func (im *Image) HasField(name string) bool {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return C.vipsx_image_has_field((*C.VipsImage)(im.ptr), cname) != 0
}

// FieldKind reports how a metadata field is stored, using the same Kind values
// as operation arguments.
func (im *Image) FieldKind(name string) Kind {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return Kind(C.vipsx_image_get_kind((*C.VipsImage)(im.ptr), cname))
}

// GetInt reads an integer metadata field.
func (im *Image) GetInt(name string) (int, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var out C.int
	if C.vipsx_image_get_int((*C.VipsImage)(im.ptr), cname, &out) != 0 {
		return 0, fieldErr(name)
	}
	return int(out), nil
}

// GetDouble reads a floating point metadata field.
func (im *Image) GetDouble(name string) (float64, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var out C.double
	if C.vipsx_image_get_double((*C.VipsImage)(im.ptr), cname, &out) != 0 {
		return 0, fieldErr(name)
	}
	return float64(out), nil
}

// GetString reads a string metadata field.
func (im *Image) GetString(name string) (string, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cstr := C.vipsx_image_get_string((*C.VipsImage)(im.ptr), cname)
	if cstr == nil {
		return "", fieldErr(name)
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr), nil
}

// GetAsString renders any metadata field as text, whatever its stored type.
// Useful for dumping EXIF without knowing each tag's type up front.
func (im *Image) GetAsString(name string) (string, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cstr := C.vipsx_image_get_as_string((*C.VipsImage)(im.ptr), cname)
	if cstr == nil {
		return "", fieldErr(name)
	}
	defer C.vipsx_gfree(unsafe.Pointer(cstr))
	return C.GoString(cstr), nil
}

// GetBlob reads a binary metadata field, such as exif-data or icc-profile-data.
func (im *Image) GetBlob(name string) ([]byte, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var length C.size_t
	data := C.vipsx_image_get_blob((*C.VipsImage)(im.ptr), cname, &length)
	if data == nil {
		return nil, fieldErr(name)
	}
	defer C.free(data)
	return C.GoBytes(data, C.int(length)), nil
}

// GetFloats reads an array-of-double metadata field.
func (im *Image) GetFloats(name string) ([]float64, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var arr *C.double
	var n C.int
	if C.vipsx_image_get_array_double((*C.VipsImage)(im.ptr), cname, &arr, &n) != 0 {
		return nil, fieldErr(name)
	}
	defer C.free(unsafe.Pointer(arr))

	out := make([]float64, int(n))
	for i, v := range unsafe.Slice(arr, int(n)) {
		out[i] = float64(v)
	}
	return out, nil
}

// GetInts reads an array-of-int metadata field.
func (im *Image) GetInts(name string) ([]int, error) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var arr *C.int
	var n C.int
	if C.vipsx_image_get_array_int((*C.VipsImage)(im.ptr), cname, &arr, &n) != 0 {
		return nil, fieldErr(name)
	}
	defer C.free(unsafe.Pointer(arr))

	out := make([]int, int(n))
	for i, v := range unsafe.Slice(arr, int(n)) {
		out[i] = int(v)
	}
	return out, nil
}

// SetInt writes an integer metadata field.
//
// The setters below change the image header in place. An image that came out of
// an operation may be shared: libvips caches built operations, so two callers
// asking for the same thing receive the same object, and mutating it from more
// than one goroutine corrupts the field list. Take a private copy first when
// the image did not come straight from a loader:
//
//	own, err := vips.Copy(im, nil)
//	own.SetString("comment", "mine")
func (im *Image) SetInt(name string, value int) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.vipsx_image_set_int((*C.VipsImage)(im.ptr), cname, C.int(value))
}

// SetDouble writes a floating point metadata field.
func (im *Image) SetDouble(name string, value float64) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.vipsx_image_set_double((*C.VipsImage)(im.ptr), cname, C.double(value))
}

// SetString writes a string metadata field.
func (im *Image) SetString(name, value string) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	cvalue := C.CString(value)
	defer C.free(unsafe.Pointer(cvalue))
	C.vipsx_image_set_string((*C.VipsImage)(im.ptr), cname, cvalue)
}

// SetBlob writes a binary metadata field. The image takes its own copy.
func (im *Image) SetBlob(name string, data []byte) {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}
	C.vipsx_image_set_blob((*C.VipsImage)(im.ptr), cname, p, C.size_t(len(data)))
	runtime.KeepAlive(data)
}

// RemoveField deletes a metadata field, reporting whether it was there.
func (im *Image) RemoveField(name string) bool {
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return C.vipsx_image_remove((*C.VipsImage)(im.ptr), cname) != 0
}

// Orientation reports the EXIF orientation tag, defaulting to 1 when absent.
// This is what autorot acts on.
func (im *Image) Orientation() int {
	if v, err := im.GetInt("orientation"); err == nil {
		return v
	}
	return 1
}

// Pages reports how many pages or frames the image carries, defaulting to 1.
func (im *Image) Pages() int {
	if v, err := im.GetInt("n-pages"); err == nil {
		return v
	}
	return 1
}

// PageHeight reports the height of one page in a multi-page image.
func (im *Image) PageHeight() int {
	if v, err := im.GetInt("page-height"); err == nil {
		return v
	}
	return im.Height()
}

// HasProfile reports whether an embedded ICC profile is present.
func (im *Image) HasProfile() bool { return im.HasField("icc-profile-data") }

// Profile returns the embedded ICC profile.
func (im *Image) Profile() ([]byte, error) { return im.GetBlob("icc-profile-data") }

// HasEXIF reports whether EXIF data is present.
func (im *Image) HasEXIF() bool { return im.HasField("exif-data") }

// EXIF returns every exif-* field rendered as text, keyed by field name.
func (im *Image) EXIF() map[string]string {
	out := map[string]string{}
	for _, name := range im.Fields() {
		if len(name) < 5 || name[:5] != "exif-" {
			continue
		}
		if v, err := im.GetAsString(name); err == nil {
			out[name] = v
		}
	}
	return out
}

func fieldErr(name string) error {
	return fmt.Errorf("vips: no metadata field %q: %s", name, lastError())
}
