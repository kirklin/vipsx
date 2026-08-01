// Routing GLib and libvips diagnostics to the caller's logger.
//
// libvips complains through GLib's logging, and GLib's default handler writes
// to stderr. In a service that is the one place the message will not be seen:
// it misses the request id, the log level, and the aggregator. There is no
// libvips call for this — the knob belongs to GLib — so this is the one shim
// here that wraps something outside libvips.

#include "vipsx.h"

#include "_cgo_export.h"

static void vipsx_log_handler(const gchar *domain, GLogLevelFlags level,
                              const gchar *message, gpointer user) {
  (void)user;
  vipsxGoLog((char *)(domain ? domain : ""), (int)level,
             (char *)(message ? message : ""));
}

// g_log_set_default_handler catches every domain that has no handler of its
// own, which is what libvips and GLib both use. It is process-wide, and the Go
// side says so.
void vipsx_log_capture(int on) {
  if (on)
    g_log_set_default_handler(vipsx_log_handler, NULL);
  else
    g_log_set_default_handler(g_log_default_handler, NULL);
}
