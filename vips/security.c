// Hardening switches, for a process that decodes images it did not choose.
//
// libvips reaches a long way down into third-party decoders, and the ones it
// marks untrusted are the ones whose upstreams have the worst record. Turning
// them off, or allowing only the formats a service actually serves, is a
// smaller attack surface bought with one call.

#include "vipsx.h"

// These arrived together in 8.13, one release under the floor, so in practice
// the guards never fire. They are here because the floor moves and the failure
// mode without them is a link error rather than a message.

int vipsx_block_untrusted_set(int state) {
#if VIPSX_AT_LEAST(8, 13)
  vips_block_untrusted_set(state);
  return 1;
#else
  (void)state;
  return 0;
#endif
}

int vipsx_operation_block_set(const char *name, int state) {
#if VIPSX_AT_LEAST(8, 13)
  vips_operation_block_set(name, state);
  return 1;
#else
  (void)name;
  (void)state;
  return 0;
#endif
}

int vipsx_pipe_read_limit_set(gint64 limit) {
#if VIPSX_AT_LEAST(8, 13)
  vips_pipe_read_limit_set(limit);
  return 1;
#else
  (void)limit;
  return 0;
#endif
}
