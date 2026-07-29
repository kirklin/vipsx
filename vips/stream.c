// Custom sources and targets, so an image can be read from an io.Reader and
// written to an io.Writer without going through a file or a buffer.
//
// libvips asks for bytes through GObject signals. The handlers here forward to
// Go, identified by a number rather than a pointer: cgo does not allow a Go
// pointer to be stored on the C side, so the Go end keeps a registry and this
// end carries only the key.

#include "vipsx.h"

#include "_cgo_export.h"

// The signal handlers. Each is called by libvips with the id as user data.

static gint64 vipsx_on_read(VipsSourceCustom *source, void *buffer,
                            gint64 length, gpointer user) {
  return vipsxGoRead((guint64)GPOINTER_TO_SIZE(user), buffer, length);
}

static gint64 vipsx_on_seek(VipsSourceCustom *source, gint64 offset, int whence,
                            gpointer user) {
  return vipsxGoSeek((guint64)GPOINTER_TO_SIZE(user), offset, whence);
}

static gint64 vipsx_on_write(VipsTargetCustom *target, const void *data,
                             gint64 length, gpointer user) {
  return vipsxGoWrite((guint64)GPOINTER_TO_SIZE(user), (void *)data, length);
}

static int vipsx_on_end(VipsTargetCustom *target, gpointer user) {
  return vipsxGoEnd((guint64)GPOINTER_TO_SIZE(user));
}

// vipsx_source_custom_new builds a source that pulls from Go.
//
// seekable decides whether the seek handler is connected at all. That is not a
// detail: a source with no seek handler makes libvips buffer what it needs and
// take the sequential path, which is what an ordinary network stream can
// support. Connecting a handler that then fails would instead have libvips
// believe seeking works and misread the file.
VipsSourceCustom *vipsx_source_custom_new(guint64 id, int seekable) {
  VipsSourceCustom *source = vips_source_custom_new();
  if (!source)
    return NULL;

  g_signal_connect(source, "read", G_CALLBACK(vipsx_on_read),
                   GSIZE_TO_POINTER((gsize)id));
  if (seekable)
    g_signal_connect(source, "seek", G_CALLBACK(vipsx_on_seek),
                     GSIZE_TO_POINTER((gsize)id));

  return source;
}

VipsTargetCustom *vipsx_target_custom_new(guint64 id) {
  VipsTargetCustom *target = vips_target_custom_new();
  if (!target)
    return NULL;

  g_signal_connect(target, "write", G_CALLBACK(vipsx_on_write),
                   GSIZE_TO_POINTER((gsize)id));
  g_signal_connect(target, "end", G_CALLBACK(vipsx_on_end),
                   GSIZE_TO_POINTER((gsize)id));

  return target;
}
