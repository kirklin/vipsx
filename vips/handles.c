// The non-image objects an operation can take as an argument, plus the process
// -wide caches and counters that go with them.

#include "vipsx.h"

// Interpolators, sources and targets.
VipsInterpolate *vipsx_interpolate_new(const char *nickname) {
  return vips_interpolate_new(nickname);
}

VipsSource *vipsx_source_new_from_file(const char *filename) {
  return vips_source_new_from_file(filename);
}

VipsSource *vipsx_source_new_from_memory(const void *data, size_t len) {
  // The blob owns a copy, so the caller's buffer need not outlive the source.
  VipsBlob *blob = vips_blob_copy(data, len);
  VipsSource *source = vips_source_new_from_blob(blob);
  vips_area_unref(VIPS_AREA(blob));
  return source;
}

VipsTarget *vipsx_target_new_to_file(const char *filename) {
  return vips_target_new_to_file(filename);
}

VipsTarget *vipsx_target_new_to_memory(void) {
  return vips_target_new_to_memory();
}

// A memory target accumulates into its "blob" property; steal only applies to
// targets backed by a descriptor. Read the property, and copy out of it so the
// caller owns something with a lifetime it controls.
void *vipsx_target_steal(VipsTarget *target, size_t *len) {
  // Flushing a target was vips_target_finish until 8.13 renamed it to
  // vips_target_end, and the old name is gone from recent headers. Neither
  // spelling works everywhere, so pick by version rather than by hope.
#if VIPS_MAJOR_VERSION > 8 || (VIPS_MAJOR_VERSION == 8 && VIPS_MINOR_VERSION >= 13)
  vips_target_end(target);
#else
  vips_target_finish(target);
#endif

  VipsBlob *blob = NULL;
  g_object_get(target, "blob", &blob, NULL);
  if (!blob) {
    *len = 0;
    return NULL;
  }

  size_t blob_len = 0;
  const void *data = vips_blob_get(blob, &blob_len);
  if (!data || blob_len == 0) {
    vips_area_unref(VIPS_AREA(blob));
    *len = 0;
    return NULL;
  }

  void *copy = g_malloc(blob_len);
  memcpy(copy, data, blob_len);
  *len = blob_len;
  vips_area_unref(VIPS_AREA(blob));
  return copy;
}

void vipsx_object_unref(void *obj) { g_object_unref(obj); }

void vipsx_gfree(void *p) { g_free(p); }

void vipsx_strv_gfree(char **strv) { g_strfreev(strv); }

// Allocation counters.
void vipsx_cache_set_max(int max) { vips_cache_set_max(max); }

void vipsx_cache_set_max_mem(size_t bytes) { vips_cache_set_max_mem(bytes); }

int vipsx_cache_get_max(void) { return vips_cache_get_max(); }

int vipsx_cache_get_size(void) { return vips_cache_get_size(); }

// The cache has three limits, not two: operations, bytes, and the file
// descriptors those operations are holding open. Whichever is reached first
// starts evicting, so a service that runs out of descriptors while well under
// the other two has no way to say so without this one.
void vipsx_cache_set_max_files(int max) { vips_cache_set_max_files(max); }

int vipsx_cache_get_max_files(void) { return vips_cache_get_max_files(); }

size_t vipsx_cache_get_max_mem(void) { return vips_cache_get_max_mem(); }

// Drain the operation cache.
//
// Not vips_cache_drop_all: in libvips 8.18 that destroys the cache hash table
// and nothing re-creates it, so every later operation logs a GLib assertion and
// leaks the reference the cache would have owned. Measured here, that is one
// operation and one pixel buffer per call, for the life of the process.
// Shrinking the limit to zero evicts through the normal path and leaves the
// table intact, and restoring the limit puts things back as they were.
void vipsx_cache_drop_all(void) {
  int max = vips_cache_get_max();
  vips_cache_set_max(0);
  vips_cache_set_max(max);
}

size_t vipsx_tracked_mem(void) { return vips_tracked_get_mem(); }

size_t vipsx_tracked_mem_highwater(void) { return vips_tracked_get_mem_highwater(); }

int vipsx_tracked_allocs(void) { return vips_tracked_get_allocs(); }

int vipsx_tracked_files(void) { return vips_tracked_get_files(); }
