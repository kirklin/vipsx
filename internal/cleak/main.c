// A leak harness for the C core, with no Go in the picture.
//
// This exists because Go's -asan does not run LeakSanitizer. That was not a
// guess: the probe deliberately lost two thousand allocations under
// detect_leaks=1 with verbosity turned up, and the run reported nothing and
// exited zero. golang/go#67833, the proposal to make LeakSanitizer usable from
// Go, is still open.
//
// The soak suite next door watches libvips' own allocation counters, which
// covers everything libvips allocates. What it cannot see is this package's own
// malloc and strdup calls: the argument specs in introspect.c, the output copies
// in call.c, the metadata strings in metadata.c. Those are plain C allocations
// and they are what this harness is for.
//
// Build and run:
//
//	make cleak
//
// Pass --leak to make it lose memory on purpose, which is how CI proves the
// checker is switched on before believing a clean report.

#include "vipsx.h"

#include <stdio.h>
#include <string.h>

static int failures = 0;

#define CHECK(cond, msg)                                                       \
  do {                                                                         \
    if (!(cond)) {                                                             \
      fprintf(stderr, "FAIL: %s: %s\n", (msg), vips_error_buffer());           \
      vips_error_clear();                                                      \
      failures++;                                                              \
      return -1;                                                               \
    }                                                                          \
  } while (0)

// make_image builds a small image without touching the filesystem.
static VipsImage *make_image(void) {
  VipsxArg args[3];
  VipsxOut outs[1];
  memset(args, 0, sizeof(args));
  memset(outs, 0, sizeof(outs));

  args[0].name = "width";
  args[0].i = 64;
  args[1].name = "height";
  args[1].i = 48;
  args[2].name = "bands";
  args[2].i = 3;
  outs[0].name = "out";

  if (vipsx_call("black", args, 3, outs, 1) != 0)
    return NULL;

  VipsImage *image = (VipsImage *)outs[0].p;
  vipsx_out_clear(outs, 1);
  return image;
}

// exercise_call runs an operation that returns an image, and one that returns a
// blob, which is the path that copies bytes out of a VipsBlob.
static int exercise_call(VipsImage *image) {
  VipsxArg args[3];
  VipsxOut outs[1];

  // linear: array in, image out
  double a[] = {1.0, 1.0, 1.0};
  double b[] = {10.0, 20.0, 30.0};
  memset(args, 0, sizeof(args));
  memset(outs, 0, sizeof(outs));
  args[0].name = "in";
  args[0].p = image;
  args[1].name = "a";
  args[1].arr = a;
  args[1].n = 3;
  args[2].name = "b";
  args[2].arr = b;
  args[2].n = 3;
  outs[0].name = "out";
  CHECK(vipsx_call("linear", args, 3, outs, 1) == 0, "linear");

  VipsImage *bright = (VipsImage *)outs[0].p;
  vipsx_out_clear(outs, 1);

  // pngsave_buffer: blob out, so vipsx_get_one mallocs and memcpys
  memset(args, 0, sizeof(args));
  memset(outs, 0, sizeof(outs));
  args[0].name = "in";
  args[0].p = bright;
  outs[0].name = "buffer";
  if (vipsx_call("pngsave_buffer", args, 1, outs, 1) != 0) {
    g_object_unref(bright);
    CHECK(0, "pngsave_buffer");
  }
  vipsx_out_clear(outs, 1);

  // getpoint: array out, the other malloc-and-copy path
  memset(args, 0, sizeof(args));
  memset(outs, 0, sizeof(outs));
  args[0].name = "in";
  args[0].p = bright;
  args[1].name = "x";
  args[1].i = 4;
  args[2].name = "y";
  args[2].i = 4;
  outs[0].name = "out-array";
  if (vipsx_call("getpoint", args, 3, outs, 1) == 0)
    vipsx_out_clear(outs, 1);
  else
    vips_error_clear();

  g_object_unref(bright);
  return 0;
}

// exercise_introspection covers every allocation introspect.c makes.
static int exercise_introspection(void) {
  VipsxOpSpec *spec = vipsx_op_spec("thumbnail");
  CHECK(spec != NULL, "op_spec");
  CHECK(spec->n_args > 0, "op_spec has no arguments");
  vipsx_op_spec_free(spec);

  int n = 0;
  char **ops = vipsx_list_operations(&n);
  CHECK(ops != NULL && n > 0, "list_operations");
  vipsx_strv_free(ops, n);

  int nvalues = 0;
  VipsxEnumValue *values = vipsx_enum_values("VipsInteresting", &nvalues);
  CHECK(values != NULL && nvalues > 0, "enum_values");
  vipsx_enum_values_free(values, nvalues);

  // A type that does not exist must not allocate anything.
  int none = 0;
  VipsxEnumValue *empty = vipsx_enum_values("VipsNoSuchType", &none);
  CHECK(empty == NULL && none == 0, "enum_values on an unknown type");

  return 0;
}

// exercise_metadata covers the strdup and malloc paths in metadata.c.
static int exercise_metadata(VipsImage *image) {
  int n = 0;
  char **fields = vipsx_image_fields(image, &n);
  CHECK(fields != NULL, "image_fields");
  vipsx_strv_gfree(fields);

  vipsx_image_set_string(image, "cleak-string", "value");
  char *s = vipsx_image_get_string(image, "cleak-string");
  CHECK(s != NULL, "get_string");
  free(s);

  char *as = vipsx_image_get_as_string(image, "cleak-string");
  CHECK(as != NULL, "get_as_string");
  vipsx_gfree(as);

  const char payload[] = "0123456789";
  vipsx_image_set_blob(image, "cleak-blob", payload, sizeof(payload));
  size_t len = 0;
  void *blob = vipsx_image_get_blob(image, "cleak-blob", &len);
  CHECK(blob != NULL && len == sizeof(payload), "get_blob");
  free(blob);

  // Reading a field that is not there must not allocate.
  char *missing = vipsx_image_get_string(image, "cleak-absent");
  CHECK(missing == NULL, "get_string on a missing field");
  vips_error_clear();

  vipsx_image_remove(image, "cleak-string");
  vipsx_image_remove(image, "cleak-blob");
  return 0;
}

// exercise_errors drives the failure paths, which have their own frees.
static int exercise_errors(VipsImage *image) {
  VipsxArg args[2];
  VipsxOut outs[1];

  // an argument that does not exist
  memset(args, 0, sizeof(args));
  memset(outs, 0, sizeof(outs));
  args[0].name = "in";
  args[0].p = image;
  args[1].name = "no-such-argument";
  args[1].i = 1;
  outs[0].name = "out";
  CHECK(vipsx_call("gaussblur", args, 2, outs, 1) != 0,
        "a bad argument name should fail");
  vips_error_clear();

  // an operation that does not exist
  CHECK(vipsx_call("no_such_operation", NULL, 0, NULL, 0) != 0,
        "an unknown operation should fail");
  vips_error_clear();

  // rejected by libvips itself
  memset(args, 0, sizeof(args));
  memset(outs, 0, sizeof(outs));
  args[0].name = "input";
  args[0].p = image;
  args[1].name = "left";
  args[1].i = 1 << 20;
  outs[0].name = "out";
  if (vipsx_call("extract_area", args, 2, outs, 1) == 0)
    vipsx_out_clear(outs, 1);
  vips_error_clear();

  return 0;
}

static int round_trip(void) {
  VipsImage *image = make_image();
  CHECK(image != NULL, "black");

  if (exercise_call(image) != 0 || exercise_introspection() != 0 ||
      exercise_metadata(image) != 0 || exercise_errors(image) != 0) {
    g_object_unref(image);
    return -1;
  }

  g_object_unref(image);
  return 0;
}

int main(int argc, char **argv) {
  int prove_the_checker_works = argc > 1 && strcmp(argv[1], "--leak") == 0;

  if (vipsx_init("cleak") != 0) {
    fprintf(stderr, "vips_init: %s\n", vips_error_buffer());
    return 1;
  }

  const int rounds = 50;
  for (int i = 0; i < rounds; i++) {
    if (round_trip() != 0) {
      fprintf(stderr, "round %d failed\n", i);
      return 1;
    }
  }

  if (prove_the_checker_works) {
    // Lose memory on purpose. A clean report from a checker that is switched
    // off looks exactly like a clean report from one that is switched on, so
    // CI runs this mode first and requires it to fail.
    for (int i = 0; i < 100; i++) {
      char *lost = malloc(1024);
      lost[0] = (char)i;
      (void)lost;
    }
    fprintf(stderr, "leaked 100 blocks on purpose; the checker must report them\n");
  }

  printf("%d rounds through the C core, %d failures\n", rounds, failures);

  // Not vips_shutdown: it frees the caches the leak checker would otherwise
  // report, which would hide a real leak behind a tidy exit. Leaving them is
  // what the suppression file is for.
  return failures == 0 ? 0 : 1;
}
