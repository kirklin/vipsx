// vipsx C core: a single generic entry point for every libvips operation.
//
// There is deliberately no per-operation code here. libvips describes its own
// operations at runtime through the GObject type system, so one call path plus
// one type switch covers every operation that exists now or is added later.

#ifndef VIPSX_H
#define VIPSX_H

#include <glib-object.h>
#include <stdlib.h>
#include <string.h>
#include <vips/vips.h>

// The complete type surface of the libvips operation API. Every argument of
// every operation is one of these; there is no eighteenth case.
#define VIPSX_KIND_UNKNOWN 0
#define VIPSX_KIND_BOOL 1
#define VIPSX_KIND_INT 2
#define VIPSX_KIND_UINT64 3
#define VIPSX_KIND_DOUBLE 4
#define VIPSX_KIND_STRING 5
#define VIPSX_KIND_REFSTRING 6
#define VIPSX_KIND_ENUM 7
#define VIPSX_KIND_FLAGS 8
#define VIPSX_KIND_IMAGE 9
#define VIPSX_KIND_ARRAY_INT 10
#define VIPSX_KIND_ARRAY_DOUBLE 11
#define VIPSX_KIND_ARRAY_IMAGE 12
#define VIPSX_KIND_BLOB 13
#define VIPSX_KIND_SOURCE 14
#define VIPSX_KIND_TARGET 15
#define VIPSX_KIND_INTERPOLATE 16
#define VIPSX_KIND_OBJECT 17

// Argument flags, mirrored from VipsArgumentFlags so Go does not need to
// include libvips headers to interpret them.
#define VIPSX_FLAG_REQUIRED 1
#define VIPSX_FLAG_INPUT 2
#define VIPSX_FLAG_OUTPUT 4
#define VIPSX_FLAG_DEPRECATED 8

// One input argument. A single flat struct so an entire call crosses the cgo
// boundary once, not once per argument.
typedef struct _VipsxArg {
  const char *name;
  int kind;
  gint64 i;    // bool, int, uint64, enum, flags
  double d;    // double
  const char *s; // string, refstring
  void *p;     // GObject-derived: image, source, target, interpolate; or blob data
  void *arr;   // int*, double*, or VipsImage** for the array kinds
  size_t n;    // array element count, or blob byte length
} VipsxArg;

// One output value. Go supplies name; C fills in kind and the payload. Any
// heap allocation reported here is released by vipsx_out_clear.
typedef struct _VipsxOut {
  const char *name;
  int kind;
  gint64 i;
  double d;
  char *s;   // C-allocated, owned by the caller
  void *p;   // GObject-derived, carries one reference owned by the caller
  void *arr; // C-allocated copy, owned by the caller
  size_t n;
} VipsxOut;

// Static description of one operation argument, from GObject introspection.
//
// The default is reported for documentation and tooling only. Nothing in the
// call path consults it: an argument is sent when supplied and omitted when
// not, so the default never has to stand in for "unset".
typedef struct _VipsxArgSpec {
  char *name;
  char *blurb;
  int kind;
  int flags;
  char *type_name; // GType name, e.g. "VipsInteresting"
  int has_default;
  gint64 i_default;
  double d_default;
  char *s_default;
} VipsxArgSpec;

typedef struct _VipsxOpSpec {
  char *name;
  char *description;
  VipsxArgSpec *args;
  int n_args;
} VipsxOpSpec;

typedef struct _VipsxEnumValue {
  char *name;
  char *nick;
  int value;
} VipsxEnumValue;

int vipsx_init(const char *name);
void vipsx_shutdown(void);
const char *vipsx_version_string(void);
int vipsx_version(int which);

// Classify a GType into one of the VIPSX_KIND_* constants. Returns
// VIPSX_KIND_UNKNOWN for anything outside the known surface, which callers
// must treat as a hard error rather than guessing.
int vipsx_kind_of_gtype(GType t);

// Worker thread count. Some reductions pick between equally valid answers
// based on which thread got there first, so pinning this makes them repeatable.
void vipsx_concurrency_set(int n);
int vipsx_concurrency_get(void);

// Invoke an operation. Sets every supplied argument unconditionally: there is
// no sentinel value and no "skip if zero" branch, so an explicit 0, false or ""
// reaches libvips exactly as written.
//
// Returns 0 on success. On failure the libvips error buffer holds the reason.
int vipsx_call(const char *operation, VipsxArg *args, int n_args, VipsxOut *outs,
               int n_outs);

// Release any heap memory reported in an output array. Does not touch the
// GObject references in .p, which transfer to the caller.
void vipsx_out_clear(VipsxOut *outs, int n_outs);

// Introspection.
VipsxOpSpec *vipsx_op_spec(const char *operation);
void vipsx_op_spec_free(VipsxOpSpec *spec);
char **vipsx_list_operations(int *count);
void vipsx_strv_free(char **strv, int count);
// Force every operation class to be initialised, which registers the enum and
// flags types their arguments use. GObject registers types lazily, so a type
// nothing has touched yet cannot be looked up by name.
void vipsx_ensure_types(void);
VipsxEnumValue *vipsx_enum_values(const char *type_name, int *count);
void vipsx_enum_values_free(VipsxEnumValue *values, int count);

// Loader and saver selection. These are not operations, they are the lookup
// libvips uses to decide which operation can read or write a given thing, so
// they have to be called directly rather than through vipsx_call.
const char *vipsx_find_load(const char *filename);
const char *vipsx_find_load_buffer(const void *buf, size_t len);
const char *vipsx_find_save(const char *filename);
const char *vipsx_find_save_buffer(const char *suffix);

// Image helpers that are pure plumbing rather than operations.
void vipsx_image_unref(VipsImage *image);
int vipsx_image_width(VipsImage *image);
int vipsx_image_height(VipsImage *image);
int vipsx_image_bands(VipsImage *image);

char *vipsx_error_buffer_copy(void);

#endif
