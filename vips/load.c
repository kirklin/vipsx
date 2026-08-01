// Choosing a loader or saver. These are lookups rather than operations, so they
// are called directly instead of through vipsx_call.

#include "vipsx.h"

// libvips names its loaders and savers after the operation that does the work,
// so these return something that can be handed straight to vipsx_call.
const char *vipsx_nickname_for(const char *class_name) {
  if (!class_name)
    return NULL;
  GType type = g_type_from_name(class_name);
  if (type == 0)
    return class_name; // not a registered class; hand it back unchanged
  const char *nickname = vips_nickname_find(type);
  return nickname ? nickname : class_name;
}

const char *vipsx_find_load(const char *filename) {
  return vips_foreign_find_load(filename);
}

const char *vipsx_find_load_buffer(const void *buf, size_t len) {
  return vips_foreign_find_load_buffer(buf, len);
}

const char *vipsx_find_save(const char *filename) {
  return vips_foreign_find_save(filename);
}

const char *vipsx_find_save_buffer(const char *suffix) {
  return vips_foreign_find_save_buffer(suffix);
}

// The stream pair. Sniffing a source rewinds it afterwards, so the loader that
// runs next still reads from the start.
const char *vipsx_find_load_source(VipsSource *source) {
  return vips_foreign_find_load_source(source);
}

const char *vipsx_find_save_target(const char *suffix) {
  return vips_foreign_find_save_target(suffix);
}
