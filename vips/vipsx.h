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

// The floor is what CI actually tests against, which is Debian 12's 8.14.
//
// 8.12 was tried and dropped: it links after a small amount of conditional
// compilation, then dies with a stack smash inside libvips partway through the
// differential suite. A binding cannot fix that from the outside, and Ubuntu
// 22.04, the only common distribution still carrying 8.12, leaves support in
// April 2027. Debian 12 carries 8.14 and is supported for considerably longer,
// so that is where the line sits.
#if VIPS_MAJOR_VERSION < 8 || (VIPS_MAJOR_VERSION == 8 && VIPS_MINOR_VERSION < 14)
#error "vipsx needs libvips 8.14 or newer"
#endif

// Feature test for everything above the floor.
//
// The floor is not the ceiling: libvips keeps adding, and a binding that can
// only use the intersection of every supported version is stuck at the oldest
// one forever. Guarding a feature lets it be used where it exists and reported
// as missing where it does not, rather than forcing the floor up.
//
// Where a guard compiles a function out, its shim returns 0 and the Go side
// turns that into an error naming the version needed. Nothing silently does
// nothing.
#define VIPSX_AT_LEAST(major, minor)                                           \
  (VIPS_MAJOR_VERSION > (major) ||                                             \
   (VIPS_MAJOR_VERSION == (major) && VIPS_MINOR_VERSION >= (minor)))

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
//
// They answer with a GObject class name such as VipsForeignLoadHeifFile, while
// everything else in this package speaks operation nicknames such as heifload.
// vipsx_nickname_for translates, so callers only ever see one naming scheme.
const char *vipsx_nickname_for(const char *class_name);
const char *vipsx_find_load(const char *filename);
const char *vipsx_find_load_buffer(const void *buf, size_t len);
const char *vipsx_find_save(const char *filename);
const char *vipsx_find_save_buffer(const char *suffix);

// Image helpers that are pure plumbing rather than operations.
void vipsx_image_unref(VipsImage *image);
int vipsx_image_width(VipsImage *image);
int vipsx_image_height(VipsImage *image);
int vipsx_image_bands(VipsImage *image);
int vipsx_image_format(VipsImage *image);
int vipsx_image_interpretation(VipsImage *image);
int vipsx_image_coding(VipsImage *image);
double vipsx_image_xres(VipsImage *image);
double vipsx_image_yres(VipsImage *image);
int vipsx_image_xoffset(VipsImage *image);
int vipsx_image_yoffset(VipsImage *image);
int vipsx_image_has_alpha(VipsImage *image);

// Image metadata. These read and write fields on the image header rather than
// running an operation, so they sit outside vipsx_call.
char **vipsx_image_fields(VipsImage *image, int *count);
int vipsx_image_has_field(VipsImage *image, const char *name);
int vipsx_image_get_kind(VipsImage *image, const char *name);
int vipsx_image_get_int(VipsImage *image, const char *name, int *out);
int vipsx_image_get_double(VipsImage *image, const char *name, double *out);
char *vipsx_image_get_string(VipsImage *image, const char *name);
char *vipsx_image_get_as_string(VipsImage *image, const char *name);
void *vipsx_image_get_blob(VipsImage *image, const char *name, size_t *len);
int vipsx_image_get_array_double(VipsImage *image, const char *name, double **out,
                                 int *n);
int vipsx_image_get_array_int(VipsImage *image, const char *name, int **out, int *n);
void vipsx_image_set_int(VipsImage *image, const char *name, int value);
void vipsx_image_set_double(VipsImage *image, const char *name, double value);
void vipsx_image_set_string(VipsImage *image, const char *name, const char *value);
void vipsx_image_set_blob(VipsImage *image, const char *name, const void *data,
                          size_t len);
void vipsx_image_set_array_int(VipsImage *image, const char *name,
                               const int *values, int n);
void vipsx_image_set_array_double(VipsImage *image, const char *name,
                                  const double *values, int n);
int vipsx_image_remove(VipsImage *image, const char *name);

// Interpolators, sources and targets are GObjects an operation can take as an
// argument, so they need constructors of their own.
VipsInterpolate *vipsx_interpolate_new(const char *nickname);

// Custom sources and targets, which call back into Go for their bytes. The id
// selects which reader or writer; see stream.c for why it is a number and not a
// pointer. A source with seekable set to zero gets no seek handler at all,
// which is how libvips is told to take its sequential path.
VipsSourceCustom *vipsx_source_custom_new(guint64 id, int seekable);
VipsTargetCustom *vipsx_target_custom_new(guint64 id);
VipsSource *vipsx_source_new_from_file(const char *filename);
VipsSource *vipsx_source_new_from_memory(const void *data, size_t len);
VipsTarget *vipsx_target_new_to_file(const char *filename);
VipsTarget *vipsx_target_new_to_memory(void);
void *vipsx_target_steal(VipsTarget *target, size_t *len);
void vipsx_object_unref(void *obj);

// Release memory libvips allocated with g_malloc.
void vipsx_gfree(void *p);
void vipsx_strv_gfree(char **strv);

// Operation cache. libvips reuses built operations, which is good for
// throughput but hides leaks, so a leak check turns it off first.
void vipsx_cache_set_max(int max);
void vipsx_cache_set_max_mem(size_t bytes);
int vipsx_cache_get_max(void);
int vipsx_cache_get_size(void);
void vipsx_cache_drop_all(void);

// Live allocation counters, for leak checking under load.
size_t vipsx_tracked_mem(void);
size_t vipsx_tracked_mem_highwater(void);
int vipsx_tracked_allocs(void);
int vipsx_tracked_files(void);

char *vipsx_error_buffer_copy(void);

// Hardening, for a process that decodes images it did not choose.
//
// Each returns 1 when the underlying libvips call was made and 0 when this
// libvips is too old to have it, so the Go side can say which version is
// wanted instead of quietly doing nothing.
int vipsx_block_untrusted_set(int state);
int vipsx_operation_block_set(const char *name, int state);
int vipsx_pipe_read_limit_set(gint64 limit);

// Watching an evaluation. Enabling progress and connecting the handler happen
// together: a handler on an image that was never told to report is a silent
// no-op, and a timeout built on one would simply never fire.
gulong vipsx_watch_eval(VipsImage *image, guint64 id, gulong *posteval_id);
void vipsx_unwatch_eval(VipsImage *image, gulong eval_id, gulong posteval_id);
void vipsx_image_set_kill(VipsImage *image, int kill);
int vipsx_image_iskilled(VipsImage *image);

#endif
