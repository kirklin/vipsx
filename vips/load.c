// Choosing a loader or saver. These are lookups rather than operations, so they
// are called directly instead of through vipsx_call.

#include "vipsx.h"

// libvips names its loaders and savers after the operation that does the work,
// so these return something that can be handed straight to vipsx_call.
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
