// Image header fields and metadata, which are read and written on the header
// rather than by running an operation.

#include "vipsx.h"

// Image header accessors.
int vipsx_image_format(VipsImage *image) { return (int)image->BandFmt; }

int vipsx_image_interpretation(VipsImage *image) { return (int)image->Type; }

int vipsx_image_coding(VipsImage *image) { return (int)image->Coding; }

double vipsx_image_xres(VipsImage *image) { return image->Xres; }

double vipsx_image_yres(VipsImage *image) { return image->Yres; }

int vipsx_image_xoffset(VipsImage *image) { return image->Xoffset; }

int vipsx_image_yoffset(VipsImage *image) { return image->Yoffset; }

int vipsx_image_has_alpha(VipsImage *image) {
  return vips_image_hasalpha(image) ? 1 : 0;
}

// Image metadata.
//
// Reported through the same eighteen kinds as operation arguments, so Go has
// one type switch rather than two.
char **vipsx_image_fields(VipsImage *image, int *count) {
  char **fields = vips_image_get_fields(image);
  int n = 0;
  while (fields && fields[n])
    n++;
  *count = n;
  return fields;
}

int vipsx_image_has_field(VipsImage *image, const char *name) {
  return vips_image_get_typeof(image, name) != 0 ? 1 : 0;
}

int vipsx_image_get_kind(VipsImage *image, const char *name) {
  GType t = vips_image_get_typeof(image, name);
  if (t == 0)
    return VIPSX_KIND_UNKNOWN;
  return vipsx_kind_of_gtype(t);
}

int vipsx_image_get_int(VipsImage *image, const char *name, int *out) {
  return vips_image_get_int(image, name, out);
}

int vipsx_image_get_double(VipsImage *image, const char *name, double *out) {
  return vips_image_get_double(image, name, out);
}

char *vipsx_image_get_string(VipsImage *image, const char *name) {
  const char *s = NULL;
  if (vips_image_get_string(image, name, &s) != 0 || !s)
    return NULL;
  return vipsx_dup(s);
}

char *vipsx_image_get_as_string(VipsImage *image, const char *name) {
  char *s = NULL;
  if (vips_image_get_as_string(image, name, &s) != 0)
    return NULL;
  return s; // already g_malloc'd; freed with vipsx_free
}

void *vipsx_image_get_blob(VipsImage *image, const char *name, size_t *len) {
  const void *data = NULL;
  if (vips_image_get_blob(image, name, &data, len) != 0)
    return NULL;
  // A present-but-empty blob still has to come back as a pointer, or the Go
  // side reads the NULL as "no such field".
  void *copy = vipsx_alloc0(*len);
  if (!copy) {
    // Without a message the NULL reads as "no such field", which is the
    // wrong diagnosis for an allocator failure.
    vips_error("vipsx", "out of memory copying field '%s'", name);
    return NULL;
  }
  if (data && *len)
    memcpy(copy, data, *len);
  return copy;
}

int vipsx_image_get_array_double(VipsImage *image, const char *name,
                                 double **out, int *n) {
  double *a = NULL;
  if (vips_image_get_array_double(image, name, &a, n) != 0)
    return -1;
  *out = vipsx_alloc0((size_t)*n * sizeof(double));
  if (!*out) {
    vips_error("vipsx", "out of memory copying field '%s'", name);
    return -1;
  }
  if (a && *n > 0)
    memcpy(*out, a, (size_t)*n * sizeof(double));
  return 0;
}

int vipsx_image_get_array_int(VipsImage *image, const char *name, int **out,
                              int *n) {
  int *a = NULL;
  if (vips_image_get_array_int(image, name, &a, n) != 0)
    return -1;
  *out = vipsx_alloc0((size_t)*n * sizeof(int));
  if (!*out) {
    vips_error("vipsx", "out of memory copying field '%s'", name);
    return -1;
  }
  if (a && *n > 0)
    memcpy(*out, a, (size_t)*n * sizeof(int));
  return 0;
}

void vipsx_image_set_int(VipsImage *image, const char *name, int value) {
  vips_image_set_int(image, name, value);
}

void vipsx_image_set_double(VipsImage *image, const char *name, double value) {
  vips_image_set_double(image, name, value);
}

void vipsx_image_set_string(VipsImage *image, const char *name,
                            const char *value) {
  vips_image_set_string(image, name, value);
}

void vipsx_image_set_blob(VipsImage *image, const char *name, const void *data,
                          size_t len) {
  void *copy = g_malloc(len ? len : 1);
  if (data && len)
    memcpy(copy, data, len);
  vips_image_set_blob(image, name, (VipsCallbackFn)g_free, copy, len);
}

void vipsx_image_set_array_int(VipsImage *image, const char *name,
                               const int *values, int n) {
  vips_image_set_array_int(image, name, values, n);
}

void vipsx_image_set_array_double(VipsImage *image, const char *name,
                                  const double *values, int n) {
  vips_image_set_array_double(image, name, values, n);
}

int vipsx_image_remove(VipsImage *image, const char *name) {
  return vips_image_remove(image, name) ? 1 : 0;
}
