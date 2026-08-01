// Process-level setup: starting libvips, reporting its version, draining its
// error buffer, and setting the worker thread count.

#include "vipsx.h"

int vipsx_init(const char *name) { return vips_init(name); }

void vipsx_shutdown(void) { vips_shutdown(); }

const char *vipsx_version_string(void) { return vips_version_string(); }

int vipsx_version(int which) { return vips_version(which); }

// Drain the error buffer.
//
// libvips keeps one error buffer for the whole process, guarded by its own
// lock. Reading it with vips_error_buffer() and then clearing it is two trips
// through that lock with a gap in between, and a thread that fails inside the
// gap has its message thrown away by our clear. vips_error_buffer_copy does
// both under one hold, so nothing can be lost between them.
//
// It returns g_malloc'd memory; the caller of this function frees with free(),
// so copy across rather than changing that contract at the Go boundary.
char *vipsx_error_buffer_copy(void) {
#if VIPS_MAJOR_VERSION > 8 || (VIPS_MAJOR_VERSION == 8 && VIPS_MINOR_VERSION >= 10)
  char *msg = vips_error_buffer_copy();
  char *copy = strdup(msg ? msg : "");
  g_free(msg);
  return copy;
#else
  const char *buf = vips_error_buffer();
  char *copy = strdup(buf ? buf : "");
  vips_error_clear();
  return copy;
#endif
}

void vipsx_concurrency_set(int n) { vips_concurrency_set(n); }

int vipsx_concurrency_get(void) { return vips_concurrency_get(); }

// libvips hangs thread-local state off every thread that calls into it, and
// releases it here. Nothing else does: the state outlives the thread.
void vipsx_thread_shutdown(void) { vips_thread_shutdown(); }

void vipsx_leak_set(int leak) { vips_leak_set(leak); }
