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
	p := im.live("Format")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_format(p))
}

// Interpretation reports the colour interpretation, matching
// VipsInterpretation.
func (im *Image) Interpretation() int {
	p := im.live("Interpretation")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_interpretation(p))
}

// Coding reports the pixel coding, matching VipsCoding.
func (im *Image) Coding() int {
	p := im.live("Coding")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_coding(p))
}

// Resolution reports pixels per millimetre horizontally and vertically.
func (im *Image) Resolution() (x, y float64) {
	p := im.live("Resolution")
	defer runtime.KeepAlive(im)
	return float64(C.vipsx_image_xres(p)),
		float64(C.vipsx_image_yres(p))
}

// Offset reports the image origin, which some operations set.
func (im *Image) Offset() (x, y int) {
	p := im.live("Offset")
	defer runtime.KeepAlive(im)
	return int(C.vipsx_image_xoffset(p)),
		int(C.vipsx_image_yoffset(p))
}

// HasAlpha reports whether the last band is an alpha channel.
func (im *Image) HasAlpha() bool {
	p := im.live("HasAlpha")
	defer runtime.KeepAlive(im)
	return C.vipsx_image_has_alpha(p) != 0
}

// Fields lists the metadata field names carried on the image, including the
// EXIF, XMP and ICC entries a loader attached.
func (im *Image) Fields() []string {
	p := im.live("Fields")
	defer runtime.KeepAlive(im)
	var count C.int
	cfields := C.vipsx_image_fields(p, &count)
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
	p := im.live("HasField")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return C.vipsx_image_has_field(p, cname) != 0
}

// FieldKind reports how a metadata field is stored, using the same Kind values
// as operation arguments.
func (im *Image) FieldKind(name string) Kind {
	p := im.live("FieldKind")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return Kind(C.vipsx_image_get_kind(p, cname))
}

// GetInt reads an integer metadata field.
func (im *Image) GetInt(name string) (int, error) {
	p := im.live("GetInt")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var out C.int
	if C.vipsx_image_get_int(p, cname, &out) != 0 {
		return 0, fieldErr(name)
	}
	return int(out), nil
}

// GetDouble reads a floating point metadata field.
func (im *Image) GetDouble(name string) (float64, error) {
	p := im.live("GetDouble")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var out C.double
	if C.vipsx_image_get_double(p, cname, &out) != 0 {
		return 0, fieldErr(name)
	}
	return float64(out), nil
}

// GetString reads a string metadata field.
func (im *Image) GetString(name string) (string, error) {
	p := im.live("GetString")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cstr := C.vipsx_image_get_string(p, cname)
	if cstr == nil {
		return "", fieldErr(name)
	}
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr), nil
}

// GetAsString renders any metadata field as text, whatever its stored type.
// Useful for dumping EXIF without knowing each tag's type up front.
func (im *Image) GetAsString(name string) (string, error) {
	p := im.live("GetAsString")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cstr := C.vipsx_image_get_as_string(p, cname)
	if cstr == nil {
		return "", fieldErr(name)
	}
	defer C.vipsx_gfree(unsafe.Pointer(cstr))
	return C.GoString(cstr), nil
}

// GetBlob reads a binary metadata field, such as exif-data or icc-profile-data.
func (im *Image) GetBlob(name string) ([]byte, error) {
	p := im.live("GetBlob")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var length C.size_t
	data := C.vipsx_image_get_blob(p, cname, &length)
	if data == nil {
		return nil, fieldErr(name)
	}
	defer C.free(data)
	return goBytes(data, length), nil
}

// GetFloats reads an array-of-double metadata field.
func (im *Image) GetFloats(name string) ([]float64, error) {
	p := im.live("GetFloats")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var arr *C.double
	var n C.int
	if C.vipsx_image_get_array_double(p, cname, &arr, &n) != 0 {
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
	p := im.live("GetInts")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var arr *C.int
	var n C.int
	if C.vipsx_image_get_array_int(p, cname, &arr, &n) != 0 {
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
	p := im.live("SetInt")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.vipsx_image_set_int(p, cname, C.int(value))
}

// SetDouble writes a floating point metadata field.
func (im *Image) SetDouble(name string, value float64) {
	p := im.live("SetDouble")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.vipsx_image_set_double(p, cname, C.double(value))
}

// SetString writes a string metadata field.
func (im *Image) SetString(name, value string) {
	p := im.live("SetString")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	cvalue := C.CString(value)
	defer C.free(unsafe.Pointer(cvalue))
	C.vipsx_image_set_string(p, cname, cvalue)
}

// SetBlob writes a binary metadata field. The image takes its own copy.
func (im *Image) SetBlob(name string, data []byte) {
	cim := im.live("SetBlob")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}
	C.vipsx_image_set_blob(cim, cname, p, C.size_t(len(data)))
	runtime.KeepAlive(data)
}

// SetInts writes an array-of-int metadata field, such as the per-frame "delay"
// of an animated image. libvips takes its own copy.
func (im *Image) SetInts(name string, values []int) {
	cim := im.live("SetInts")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cvals := make([]C.int, len(values))
	for i, v := range values {
		cvals[i] = C.int(v)
	}
	var p *C.int
	if len(cvals) > 0 {
		p = &cvals[0]
	}
	C.vipsx_image_set_array_int(cim, cname, p, C.int(len(cvals)))
}

// SetFloats writes an array-of-double metadata field. libvips takes its own
// copy.
func (im *Image) SetFloats(name string, values []float64) {
	cim := im.live("SetFloats")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	cvals := make([]C.double, len(values))
	for i, v := range values {
		cvals[i] = C.double(v)
	}
	var p *C.double
	if len(cvals) > 0 {
		p = &cvals[0]
	}
	C.vipsx_image_set_array_double(cim, cname, p, C.int(len(cvals)))
}

// RemoveField deletes a metadata field, reporting whether it was there.
func (im *Image) RemoveField(name string) bool {
	p := im.live("RemoveField")
	defer runtime.KeepAlive(im)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return C.vipsx_image_remove(p, cname) != 0
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
func (im *Image) HasEXIF() bool { return im.HasField(exifRawBlock) }

// EXIF returns every EXIF tag rendered as text, keyed by field name.
//
// The raw exif-data block is not among them. It is a few kilobytes of binary
// that happens to share the exif- prefix, and rendering it as text produces
// several thousand characters of base64 sitting in the map next to the tags.
// Read it with GetBlob("exif-data") when the bytes themselves are wanted.
//
// Values arrive in libvips' own rendering, which appends a description of the
// stored type:
//
//	exif-ifd0-Make → "NIKON CORPORATION (NIKON CORPORATION, ASCII, 18 components, 18 bytes)"
//
// They are handed over unchanged. Splitting on the first " (" would tidy most of
// them and silently truncate any tag whose value contains a bracket, which user
// comments and lens names do, so the choice of how to parse is left to whoever
// knows what is in their files.
func (im *Image) EXIF() map[string]string {
	out := map[string]string{}
	for _, name := range im.Fields() {
		if len(name) < 5 || name[:5] != "exif-" || name == exifRawBlock {
			continue
		}
		if v, err := im.GetAsString(name); err == nil {
			out[name] = v
		}
	}
	return out
}

// exifRawBlock is the field holding the undecoded EXIF segment.
const exifRawBlock = "exif-data"

func fieldErr(name string) error {
	return fmt.Errorf("vips: no metadata field %q: %s", name, lastError())
}

// structuralFields describe the image itself rather than saying anything about
// where it came from. Removing one either fails or changes what the image is,
// so Strip leaves them alone.
//
// page-height and n-pages are here because a multi-page image stops being one
// without them, and vips-loader because libvips writes it itself. delay and
// loop are here by the same logic as page geometry: an animation without its
// timing still has every frame and plays them wrong. gif-delay and gif-loop
// are the older spellings some loaders still set alongside them.
var structuralFields = map[string]bool{
	"width": true, "height": true, "bands": true,
	"format": true, "coding": true, "interpretation": true,
	"xoffset": true, "yoffset": true, "xres": true, "yres": true,
	"filename": true, "vips-loader": true,
	"n-pages": true, "page-height": true, "loader": true,
	"concurrency": true, "background": true,
	"delay": true, "loop": true, "gif-delay": true, "gif-loop": true,
}

// MetadataFields lists the fields carrying information about the image rather
// than describing its pixels: EXIF, XMP, IPTC, the ICC profile, the orientation
// tag, and whatever else a loader attached.
//
// This is what Strip removes when given no arguments.
func (im *Image) MetadataFields() []string {
	var out []string
	for _, name := range im.Fields() {
		if !structuralFields[name] {
			out = append(out, name)
		}
	}
	return out
}

// Strip returns a copy of the image with metadata removed. Given no arguments
// it removes everything MetadataFields reports; given names, only those.
//
// Two of the fields it removes change how the result looks, and neither is
// obvious:
//
// Removing "orientation" does not straighten anything. A photograph from a
// phone is stored sideways with a tag saying which way up it goes, and dropping
// the tag leaves it sideways for good. Call Autorot first, which applies the
// rotation and then clears the tag.
//
// Removing "icc-profile-data" changes the colours of anything not already in
// sRGB, because the numbers stay and the note explaining them goes. Convert
// with colourspace first, or keep the profile.
//
//	own, err := vips.Autorot(im)          // apply the rotation
//	own, err = own.Strip()                // then drop everything
//
// A copy, not the image itself. An image that came out of an operation may be
// shared: libvips caches built operations, so two callers asking for the same
// thing receive the same object, and removing fields from it would remove them
// from under the other caller.
func (im *Image) Strip(fields ...string) (*Image, error) {
	// Call rather than the generated Copy. This file is part of the layer the
	// generator itself imports, so depending on generated code here would mean
	// the package cannot compile with the generated files absent — and they are
	// absent every time the generator is about to write them.
	//
	// The privacy of the copy rests on libvips marking copy nocache. A cached
	// operation hands the same output to every identical call, and editing a
	// header shared that way would be exactly the corruption described above.
	outs, err := Call("copy", In("in", im))
	if err != nil {
		return nil, err
	}
	out, err := outs.Image("out")
	if err != nil {
		return nil, err
	}

	if len(fields) == 0 {
		fields = out.MetadataFields()
	}
	for _, name := range fields {
		if structuralFields[name] {
			out.Close()
			return nil, fmt.Errorf(
				"vips: %q describes the image itself rather than its history "+
					"and cannot be stripped", name)
		}
		out.RemoveField(name)
	}
	return out, nil
}
