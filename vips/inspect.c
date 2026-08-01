// Asking about an image, a format or the library itself, without running an
// operation.
//
// These are the questions a caller has to answer before doing work: is this
// bigger than we accept, will the orientation swap the sides, can these pixels
// be read as bytes, can we even write the format that was asked for.

#include "vipsx.h"

// Image header questions.
//
// get_orientation_swap is the one worth naming: an EXIF orientation of 5 to 8
// means the stored width is the displayed height. Sizing a thumbnail without
// asking gets it wrong on a quarter of the photographs a phone produces.
int vipsx_image_get_orientation_swap(VipsImage *image) {
  return vips_image_get_orientation_swap(image) ? 1 : 0;
}

int vipsx_image_guess_format(VipsImage *image) {
  return (int)vips_image_guess_format(image);
}

int vipsx_image_guess_interpretation(VipsImage *image) {
  return (int)vips_image_guess_interpretation(image);
}

// Both borrow: the string belongs to the image and dies with it, so Go copies
// before returning.
const char *vipsx_image_get_filename(VipsImage *image) {
  return vips_image_get_filename(image);
}

const char *vipsx_image_get_history(VipsImage *image) {
  return vips_image_get_history(image);
}

// Band format predicates. Cheap, and the alternative is a table in Go that
// libvips would eventually disagree with.
int vipsx_band_format_is8bit(int format) {
  return vips_band_format_is8bit((VipsBandFormat)format) ? 1 : 0;
}

int vipsx_band_format_isint(int format) {
  return vips_band_format_isint((VipsBandFormat)format) ? 1 : 0;
}

int vipsx_band_format_isuint(int format) {
  return vips_band_format_isuint((VipsBandFormat)format) ? 1 : 0;
}

int vipsx_band_format_isfloat(int format) {
  return vips_band_format_isfloat((VipsBandFormat)format) ? 1 : 0;
}

int vipsx_band_format_iscomplex(int format) {
  return vips_band_format_iscomplex((VipsBandFormat)format) ? 1 : 0;
}

// 8.15 and up. Reimplementing the table for older libvips would be exactly the
// "a table that libvips will eventually disagree with" this function exists to
// avoid, so say it is missing instead.
int vipsx_interpretation_max_alpha(int interpretation, double *out) {
#if VIPSX_AT_LEAST(8, 15)
  *out = vips_interpretation_max_alpha((VipsInterpretation)interpretation);
  return 1;
#else
  (void)interpretation;
  *out = 0;
  return 0;
#endif
}

// Library limits and capabilities.
//
// The macro rather than vips_max_coord_get, which only exists in newer libvips.
// VIPS_MAX_COORD has always been there: an integer literal in older versions,
// and a call to that function in the ones that have it. Either way it is the
// right answer with no version guard to keep in step.
int vipsx_max_coord_get(void) { return VIPS_MAX_COORD; }

char **vipsx_foreign_get_suffixes(void) { return vips_foreign_get_suffixes(); }

int vipsx_foreign_flags(const char *loader, const char *filename) {
  return (int)vips_foreign_flags(loader, filename);
}

// libvips' own filename-with-options syntax, "photo.jpg[Q=90]". Both halves
// come back g_malloc'd.
char *vipsx_filename_get_filename(const char *name) {
  return vips_filename_get_filename(name);
}

char *vipsx_filename_get_options(const char *name) {
  return vips_filename_get_options(name);
}

// Releasing what a live image is holding, without ending the image.
//
// minimise_all closes the file descriptors down the pipeline; the image still
// works and reopens what it needs. invalidate_all throws away cached pixels,
// which is what you want after the file underneath has changed.
void vipsx_image_minimise_all(VipsImage *image) {
  vips_image_minimise_all(image);
}

void vipsx_image_invalidate_all(VipsImage *image) {
  vips_image_invalidate_all(image);
}

// Error suppression, for work that is expected to fail.
void vipsx_error_freeze(void) { vips_error_freeze(); }

void vipsx_error_thaw(void) { vips_error_thaw(); }

// Peek at the front of a source without consuming it. The pointer is the
// source's own buffer, so Go copies out of it.
const void *vipsx_source_sniff(VipsSource *source, size_t length) {
  return vips_source_sniff(source, length);
}

gint64 vipsx_source_length(VipsSource *source) {
  return vips_source_length(source);
}

void vipsx_source_minimise(VipsSource *source) { vips_source_minimise(source); }

void vipsx_source_unminimise(VipsSource *source) {
  vips_source_unminimise(source);
}
