// Watching an evaluation, and stopping one.
//
// libvips reports progress by emitting signals on the image progress was
// enabled on, and every image downstream of it reports there too. The handler
// below forwards each report to Go, identified by a number for the same reason
// stream.c is: cgo will not let a Go pointer be stored on the C side.
//
// Killing is the same mechanism read backwards. There is no separate "cancel"
// call in libvips: you set the kill flag from a progress handler, and the
// pipeline notices at its next check and unwinds with an error. So cancellation
// is not a feature bolted next to progress, it is progress plus one flag.

#include "vipsx.h"

#include "_cgo_export.h"

static void vipsx_on_eval(VipsImage *image, void *progress, gpointer user) {
  VipsProgress *p = (VipsProgress *)progress;

  // Go answers whether to carry on. Setting the flag here rather than from Go
  // keeps the decision on the thread libvips called us on, and means the Go
  // side never has to hold an image pointer to be able to stop it.
  if (vipsxGoEval((guint64)GPOINTER_TO_SIZE(user), (int)p->run, (int)p->eta,
                  p->tpels, p->npels, (int)p->percent) != 0)
    vips_image_set_kill(image, TRUE);
}

static void vipsx_on_posteval(VipsImage *image, void *progress, gpointer user) {
  (void)image;
  (void)progress;
  vipsxGoEvalDone((guint64)GPOINTER_TO_SIZE(user));
}

// Watch an image. Progress is enabled here rather than being left to the
// caller: connecting a handler to an image that never reports is a silent
// no-op, which is the wrong failure for something a timeout depends on.
gulong vipsx_watch_eval(VipsImage *image, guint64 id, gulong *posteval_id) {
  vips_image_set_progress(image, TRUE);
  gulong h = g_signal_connect(image, "eval", G_CALLBACK(vipsx_on_eval),
                              GSIZE_TO_POINTER((gsize)id));
  *posteval_id = g_signal_connect(image, "posteval",
                                  G_CALLBACK(vipsx_on_posteval),
                                  GSIZE_TO_POINTER((gsize)id));
  return h;
}

void vipsx_unwatch_eval(VipsImage *image, gulong eval_id, gulong posteval_id) {
  if (eval_id)
    g_signal_handler_disconnect(image, eval_id);
  if (posteval_id)
    g_signal_handler_disconnect(image, posteval_id);
  vips_image_set_progress(image, FALSE);
}

void vipsx_image_set_kill(VipsImage *image, int kill) {
  vips_image_set_kill(image, kill);
}

int vipsx_image_iskilled(VipsImage *image) {
  return vips_image_iskilled(image) ? 1 : 0;
}
