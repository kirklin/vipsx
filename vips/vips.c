// Process-level setup: starting libvips, reporting its version, draining its
// error buffer, and setting the worker thread count.

#include "vipsx.h"

int vipsx_init(const char *name) { return vips_init(name); }

void vipsx_shutdown(void) { vips_shutdown(); }

const char *vipsx_version_string(void) { return vips_version_string(); }

int vipsx_version(int which) { return vips_version(which); }

char *vipsx_error_buffer_copy(void) {
  const char *buf = vips_error_buffer();
  char *copy = strdup(buf ? buf : "");
  vips_error_clear();
  return copy;
}

void vipsx_concurrency_set(int n) { vips_concurrency_set(n); }

int vipsx_concurrency_get(void) { return vips_concurrency_get(); }
