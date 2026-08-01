// Raw pixels in and out, and materialising a pipeline.
//
// Everything else in this package moves encoded bytes: a JPEG in, a WebP out.
// These three move the pixels themselves, which is what a caller needs when
// the image came from somewhere that is not a file — another library's
// decoder, a framebuffer, a Go image.Image.

#include "vipsx.h"

// Note new_from_memory_copy rather than new_from_memory.
//
// vips_image_new_from_memory does not copy: it keeps the caller's pointer for
// the life of the image. That is the faster call and the wrong one to reach
// from Go, where the buffer belongs to a collector that is free to move or
// reclaim it. The copy is one memcpy against a decode.
VipsImage *vipsx_image_new_from_memory(const void *data, size_t size, int width,
                                       int height, int bands, int format) {
  return vips_image_new_from_memory_copy(data, size, width, height, bands,
                                         (VipsBandFormat)format);
}

// Renders the pipeline and hands back the pixels, band-packed by row. The
// buffer is g_malloc'd and belongs to the caller.
void *vipsx_image_write_to_memory(VipsImage *image, size_t *size) {
  return vips_image_write_to_memory(image, size);
}

// Renders the pipeline into memory and wraps the result as an image.
//
// libvips refs and returns the same image when it is already a memory area, so
// this is cheap when there is nothing to do.
VipsImage *vipsx_image_copy_memory(VipsImage *image) {
  return vips_image_copy_memory(image);
}

// A private, memory-backed copy, for handing to an operation that modifies its
// input in place.
//
// vips_image_copy_memory alone is not enough: on an image that is already a
// memory buffer it refs and returns the same object, which is exactly the
// sharing this exists to prevent. vips_copy always makes a new image — it is
// marked nocache, so the copy really is fresh — and materialising that copy
// yields pixels nothing else can see.
VipsImage *vipsx_image_mutable_copy(VipsImage *in) {
  VipsImage *t;
  if (vips_copy(in, &t, NULL))
    return NULL;
  VipsImage *out = vips_image_copy_memory(t);
  g_object_unref(t);
  return out;
}

guint64 vipsx_format_sizeof(int format) {
  return vips_format_sizeof((VipsBandFormat)format);
}

// The zero-copy read: renders the pipeline and hands back the image's own
// buffer rather than a copy of it. Nothing is transferred — the pointer is
// valid for as long as the image is, which is why the Go side lends it to a
// callback instead of returning it.
const void *vipsx_image_get_data(VipsImage *image) {
  return vips_image_get_data(image);
}
