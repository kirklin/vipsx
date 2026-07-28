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

// ---------------------------------------------------------------------------
// The type switch. Eighteen cases, and that is the whole of it.
//
// Ordering matters: the specific VIPS boxed and object types must be tested
// before the general G_TYPE_OBJECT fallback, or every image would classify as
// a plain object.
// ---------------------------------------------------------------------------

int vipsx_kind_of_gtype(GType t) {
  if (t == G_TYPE_BOOLEAN)
    return VIPSX_KIND_BOOL;
  if (t == G_TYPE_INT || t == G_TYPE_UINT)
    return VIPSX_KIND_INT;
  if (t == G_TYPE_INT64 || t == G_TYPE_UINT64)
    return VIPSX_KIND_UINT64;
  if (t == G_TYPE_DOUBLE || t == G_TYPE_FLOAT)
    return VIPSX_KIND_DOUBLE;
  if (t == G_TYPE_STRING)
    return VIPSX_KIND_STRING;
  if (t == VIPS_TYPE_REF_STRING)
    return VIPSX_KIND_REFSTRING;
  if (G_TYPE_IS_ENUM(t))
    return VIPSX_KIND_ENUM;
  if (G_TYPE_IS_FLAGS(t))
    return VIPSX_KIND_FLAGS;
  if (t == VIPS_TYPE_ARRAY_INT)
    return VIPSX_KIND_ARRAY_INT;
  if (t == VIPS_TYPE_ARRAY_DOUBLE)
    return VIPSX_KIND_ARRAY_DOUBLE;
  if (t == VIPS_TYPE_ARRAY_IMAGE)
    return VIPSX_KIND_ARRAY_IMAGE;
  if (t == VIPS_TYPE_BLOB)
    return VIPSX_KIND_BLOB;
  if (g_type_is_a(t, VIPS_TYPE_IMAGE))
    return VIPSX_KIND_IMAGE;
  if (g_type_is_a(t, VIPS_TYPE_SOURCE))
    return VIPSX_KIND_SOURCE;
  if (g_type_is_a(t, VIPS_TYPE_TARGET))
    return VIPSX_KIND_TARGET;
  if (g_type_is_a(t, VIPS_TYPE_INTERPOLATE))
    return VIPSX_KIND_INTERPOLATE;
  if (g_type_is_a(t, G_TYPE_OBJECT))
    return VIPSX_KIND_OBJECT;
  return VIPSX_KIND_UNKNOWN;
}

// ---------------------------------------------------------------------------
// Setting one argument.
//
// Every supplied argument is set. There is no "skip if the value is zero"
// branch anywhere in this file, which is what makes an explicit 0, false or ""
// expressible. Absence is represented by not appearing in the argument list.
// ---------------------------------------------------------------------------

static int vipsx_set_one(VipsOperation *op, VipsxArg *arg) {
  GParamSpec *pspec =
      g_object_class_find_property(G_OBJECT_GET_CLASS(op), arg->name);
  if (!pspec) {
    vips_error("vipsx", "operation has no argument '%s'", arg->name);
    return -1;
  }

  GType type = G_PARAM_SPEC_VALUE_TYPE(pspec);
  int kind = vipsx_kind_of_gtype(type);

  GValue gv = G_VALUE_INIT;
  g_value_init(&gv, type);

  switch (kind) {
  case VIPSX_KIND_BOOL:
    g_value_set_boolean(&gv, arg->i != 0);
    break;
  case VIPSX_KIND_INT:
    if (type == G_TYPE_UINT)
      g_value_set_uint(&gv, (guint)arg->i);
    else
      g_value_set_int(&gv, (gint)arg->i);
    break;
  case VIPSX_KIND_UINT64:
    if (type == G_TYPE_INT64)
      g_value_set_int64(&gv, (gint64)arg->i);
    else
      g_value_set_uint64(&gv, (guint64)arg->i);
    break;
  case VIPSX_KIND_DOUBLE:
    if (type == G_TYPE_FLOAT)
      g_value_set_float(&gv, (gfloat)arg->d);
    else
      g_value_set_double(&gv, arg->d);
    break;
  case VIPSX_KIND_STRING:
    g_value_set_string(&gv, arg->s);
    break;
  case VIPSX_KIND_REFSTRING:
    vips_value_set_ref_string(&gv, arg->s);
    break;
  case VIPSX_KIND_ENUM:
    g_value_set_enum(&gv, (gint)arg->i);
    break;
  case VIPSX_KIND_FLAGS:
    g_value_set_flags(&gv, (guint)arg->i);
    break;
  case VIPSX_KIND_IMAGE:
  case VIPSX_KIND_SOURCE:
  case VIPSX_KIND_TARGET:
  case VIPSX_KIND_INTERPOLATE:
  case VIPSX_KIND_OBJECT:
    g_value_set_object(&gv, G_OBJECT(arg->p));
    break;
  case VIPSX_KIND_ARRAY_INT:
    vips_value_set_array_int(&gv, (int *)arg->arr, (int)arg->n);
    break;
  case VIPSX_KIND_ARRAY_DOUBLE:
    vips_value_set_array_double(&gv, (double *)arg->arr, (int)arg->n);
    break;
  case VIPSX_KIND_ARRAY_IMAGE: {
    vips_value_set_array_image(&gv, (int)arg->n);
    VipsImage **out = vips_value_get_array_image(&gv, NULL);
    VipsImage **in = (VipsImage **)arg->arr;
    for (size_t i = 0; i < arg->n; i++) {
      g_object_ref(in[i]);
      out[i] = in[i];
    }
    break;
  }
  case VIPSX_KIND_BLOB: {
    // libvips may outlive the caller's buffer, so hand it memory it owns.
    void *copy = g_malloc(arg->n);
    memcpy(copy, arg->p, arg->n);
    vips_value_set_blob_free(&gv, copy, arg->n);
    break;
  }
  default:
    g_value_unset(&gv);
    vips_error("vipsx", "argument '%s' has unsupported type '%s'", arg->name,
               g_type_name(type));
    return -1;
  }

  g_object_set_property(G_OBJECT(op), arg->name, &gv);
  g_value_unset(&gv);
  return 0;
}

// ---------------------------------------------------------------------------
// Reading one output.
//
// Reference rules follow the libvips binding guide: g_value_dup_object takes
// the reference we hand to the caller, and everything heap-allocated here is
// released by vipsx_out_clear once Go has copied it.
// ---------------------------------------------------------------------------

static int vipsx_get_one(VipsOperation *op, VipsxOut *out) {
  GParamSpec *pspec =
      g_object_class_find_property(G_OBJECT_GET_CLASS(op), out->name);
  if (!pspec) {
    vips_error("vipsx", "operation has no output '%s'", out->name);
    return -1;
  }

  GType type = G_PARAM_SPEC_VALUE_TYPE(pspec);
  out->kind = vipsx_kind_of_gtype(type);

  GValue gv = G_VALUE_INIT;
  g_value_init(&gv, type);
  g_object_get_property(G_OBJECT(op), out->name, &gv);

  switch (out->kind) {
  case VIPSX_KIND_BOOL:
    out->i = g_value_get_boolean(&gv) ? 1 : 0;
    break;
  case VIPSX_KIND_INT:
    out->i = (type == G_TYPE_UINT) ? (gint64)g_value_get_uint(&gv)
                                   : (gint64)g_value_get_int(&gv);
    break;
  case VIPSX_KIND_UINT64:
    out->i = (type == G_TYPE_INT64) ? g_value_get_int64(&gv)
                                    : (gint64)g_value_get_uint64(&gv);
    break;
  case VIPSX_KIND_DOUBLE:
    out->d = (type == G_TYPE_FLOAT) ? (double)g_value_get_float(&gv)
                                    : g_value_get_double(&gv);
    break;
  case VIPSX_KIND_STRING: {
    const char *s = g_value_get_string(&gv);
    out->s = strdup(s ? s : "");
    break;
  }
  case VIPSX_KIND_REFSTRING: {
    size_t len = 0;
    const char *s = vips_value_get_ref_string(&gv, &len);
    out->s = strdup(s ? s : "");
    break;
  }
  case VIPSX_KIND_ENUM:
    out->i = (gint64)g_value_get_enum(&gv);
    break;
  case VIPSX_KIND_FLAGS:
    out->i = (gint64)g_value_get_flags(&gv);
    break;
  case VIPSX_KIND_IMAGE:
  case VIPSX_KIND_SOURCE:
  case VIPSX_KIND_TARGET:
  case VIPSX_KIND_INTERPOLATE:
  case VIPSX_KIND_OBJECT:
    out->p = g_value_dup_object(&gv);
    break;
  case VIPSX_KIND_ARRAY_INT: {
    int n = 0;
    int *a = vips_value_get_array_int(&gv, &n);
    out->n = (size_t)n;
    out->arr = malloc(n * sizeof(int));
    memcpy(out->arr, a, n * sizeof(int));
    break;
  }
  case VIPSX_KIND_ARRAY_DOUBLE: {
    int n = 0;
    double *a = vips_value_get_array_double(&gv, &n);
    out->n = (size_t)n;
    out->arr = malloc(n * sizeof(double));
    memcpy(out->arr, a, n * sizeof(double));
    break;
  }
  case VIPSX_KIND_ARRAY_IMAGE: {
    int n = 0;
    VipsImage **a = vips_value_get_array_image(&gv, &n);
    out->n = (size_t)n;
    out->arr = malloc(n * sizeof(VipsImage *));
    VipsImage **dst = (VipsImage **)out->arr;
    for (int i = 0; i < n; i++) {
      g_object_ref(a[i]);
      dst[i] = a[i];
    }
    break;
  }
  case VIPSX_KIND_BLOB: {
    size_t len = 0;
    const void *data = vips_value_get_blob(&gv, &len);
    out->n = len;
    out->arr = malloc(len);
    memcpy(out->arr, data, len);
    break;
  }
  default:
    g_value_unset(&gv);
    vips_error("vipsx", "output '%s' has unsupported type '%s'", out->name,
               g_type_name(type));
    return -1;
  }

  g_value_unset(&gv);
  return 0;
}

void vipsx_out_clear(VipsxOut *outs, int n_outs) {
  for (int i = 0; i < n_outs; i++) {
    free(outs[i].s);
    free(outs[i].arr);
    outs[i].s = NULL;
    outs[i].arr = NULL;
  }
}

int vipsx_call(const char *operation, VipsxArg *args, int n_args,
               VipsxOut *outs, int n_outs) {
  VipsOperation *op = vips_operation_new(operation);
  if (!op) {
    vips_error("vipsx", "no such operation '%s'", operation);
    return -1;
  }

  for (int i = 0; i < n_args; i++) {
    if (vipsx_set_one(op, &args[i]) != 0) {
      g_object_unref(op);
      return -1;
    }
  }

  // Goes through the operation cache, so identical calls reuse a built result.
  if (vips_cache_operation_buildp(&op)) {
    vips_object_unref_outputs(VIPS_OBJECT(op));
    g_object_unref(op);
    return -1;
  }

  for (int i = 0; i < n_outs; i++) {
    if (vipsx_get_one(op, &outs[i]) != 0) {
      vipsx_out_clear(outs, i);
      vips_object_unref_outputs(VIPS_OBJECT(op));
      g_object_unref(op);
      return -1;
    }
  }

  vips_object_unref_outputs(VIPS_OBJECT(op));
  g_object_unref(op);
  return 0;
}

// ---------------------------------------------------------------------------
// Introspection: what operations exist, and what arguments do they take.
// ---------------------------------------------------------------------------

typedef struct {
  VipsxArgSpec *args;
  int count;
  int max;
} CollectArgs;

static void *vipsx_collect_arg(VipsObject *object, GParamSpec *pspec,
                               VipsArgumentClass *argument_class,
                               VipsArgumentInstance *argument_instance, void *a,
                               void *b) {
  CollectArgs *data = (CollectArgs *)a;
  if (data->count >= data->max)
    return NULL;

  VipsxArgSpec *spec = &data->args[data->count];
  GType type = G_PARAM_SPEC_VALUE_TYPE(pspec);

  spec->name = strdup(g_param_spec_get_name(pspec));
  const char *blurb = g_param_spec_get_blurb(pspec);
  spec->blurb = strdup(blurb ? blurb : "");
  spec->kind = vipsx_kind_of_gtype(type);
  spec->type_name = strdup(g_type_name(type));

  spec->has_default = 1;
  if (G_IS_PARAM_SPEC_BOOLEAN(pspec))
    spec->i_default = G_PARAM_SPEC_BOOLEAN(pspec)->default_value ? 1 : 0;
  else if (G_IS_PARAM_SPEC_INT(pspec))
    spec->i_default = G_PARAM_SPEC_INT(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_UINT(pspec))
    spec->i_default = G_PARAM_SPEC_UINT(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_INT64(pspec))
    spec->i_default = G_PARAM_SPEC_INT64(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_UINT64(pspec))
    spec->i_default = (gint64)G_PARAM_SPEC_UINT64(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_ENUM(pspec))
    spec->i_default = G_PARAM_SPEC_ENUM(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_FLAGS(pspec))
    spec->i_default = (gint64)G_PARAM_SPEC_FLAGS(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_DOUBLE(pspec))
    spec->d_default = G_PARAM_SPEC_DOUBLE(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_FLOAT(pspec))
    spec->d_default = (double)G_PARAM_SPEC_FLOAT(pspec)->default_value;
  else if (G_IS_PARAM_SPEC_STRING(pspec)) {
    const char *d = G_PARAM_SPEC_STRING(pspec)->default_value;
    spec->s_default = strdup(d ? d : "");
  } else
    spec->has_default = 0;

  int flags = 0;
  if (argument_class->flags & VIPS_ARGUMENT_REQUIRED)
    flags |= VIPSX_FLAG_REQUIRED;
  if (argument_class->flags & VIPS_ARGUMENT_INPUT)
    flags |= VIPSX_FLAG_INPUT;
  if (argument_class->flags & VIPS_ARGUMENT_OUTPUT)
    flags |= VIPSX_FLAG_OUTPUT;
  if (argument_class->flags & VIPS_ARGUMENT_DEPRECATED)
    flags |= VIPSX_FLAG_DEPRECATED;
  spec->flags = flags;

  data->count++;
  return NULL;
}

VipsxOpSpec *vipsx_op_spec(const char *operation) {
  VipsOperation *op = vips_operation_new(operation);
  if (!op)
    return NULL;

  VipsxOpSpec *spec = calloc(1, sizeof(VipsxOpSpec));
  spec->name = strdup(operation);
  VipsObjectClass *klass = VIPS_OBJECT_GET_CLASS(op);
  spec->description = strdup(klass->description ? klass->description : "");

  const int max_args = 64;
  spec->args = calloc(max_args, sizeof(VipsxArgSpec));
  CollectArgs data = {.args = spec->args, .count = 0, .max = max_args};
  vips_argument_map(VIPS_OBJECT(op), vipsx_collect_arg, &data, NULL);
  spec->n_args = data.count;

  g_object_unref(op);
  return spec;
}

void vipsx_op_spec_free(VipsxOpSpec *spec) {
  if (!spec)
    return;
  for (int i = 0; i < spec->n_args; i++) {
    free(spec->args[i].name);
    free(spec->args[i].blurb);
    free(spec->args[i].type_name);
    free(spec->args[i].s_default);
  }
  free(spec->args);
  free(spec->name);
  free(spec->description);
  free(spec);
}

typedef struct {
  char **names;
  int count;
  int max;
} CollectOps;

static void vipsx_collect_ops(GType type, CollectOps *data) {
  if (!G_TYPE_IS_ABSTRACT(type) && data->count < data->max) {
    VipsObjectClass *klass = VIPS_OBJECT_CLASS(g_type_class_ref(type));
    if (klass && klass->nickname) {
      VipsOperation *op = VIPS_OPERATION(g_object_new(type, NULL));
      int deprecated = 0;
      if (op) {
        deprecated =
            (vips_operation_get_flags(op) & VIPS_OPERATION_DEPRECATED) != 0;
        g_object_unref(op);
      }
      if (!deprecated)
        data->names[data->count++] = strdup(klass->nickname);
    }
    g_type_class_unref(klass);
  }

  guint n_children = 0;
  GType *children = g_type_children(type, &n_children);
  for (guint i = 0; i < n_children && data->count < data->max; i++)
    vipsx_collect_ops(children[i], data);
  g_free(children);
}

char **vipsx_list_operations(int *count) {
  const int max_ops = 2000;
  CollectOps data = {
      .names = calloc(max_ops, sizeof(char *)), .count = 0, .max = max_ops};
  vipsx_collect_ops(VIPS_TYPE_OPERATION, &data);
  *count = data.count;
  return data.names;
}

void vipsx_strv_free(char **strv, int count) {
  for (int i = 0; i < count; i++)
    free(strv[i]);
  free(strv);
}

static void vipsx_touch_types(GType type) {
  if (!G_TYPE_IS_ABSTRACT(type)) {
    VipsObjectClass *klass = VIPS_OBJECT_CLASS(g_type_class_ref(type));
    if (klass) {
      // Instantiating is what installs the properties, and installing the
      // properties is what registers their enum types.
      VipsOperation *op = VIPS_OPERATION(g_object_new(type, NULL));
      if (op)
        g_object_unref(op);
      g_type_class_unref(klass);
    }
  }

  guint n_children = 0;
  GType *children = g_type_children(type, &n_children);
  for (guint i = 0; i < n_children; i++)
    vipsx_touch_types(children[i]);
  g_free(children);
}

void vipsx_ensure_types(void) { vipsx_touch_types(VIPS_TYPE_OPERATION); }

VipsxEnumValue *vipsx_enum_values(const char *type_name, int *count) {
  GType type = g_type_from_name(type_name);
  if (type == 0 || !(G_TYPE_IS_ENUM(type) || G_TYPE_IS_FLAGS(type))) {
    *count = 0;
    return NULL;
  }

  gpointer klass = g_type_class_ref(type);
  guint n_values;
  VipsxEnumValue *values;

  if (G_TYPE_IS_FLAGS(type)) {
    GFlagsClass *fc = (GFlagsClass *)klass;
    n_values = fc->n_values;
    values = calloc(n_values, sizeof(VipsxEnumValue));
    for (guint i = 0; i < n_values; i++) {
      values[i].name = strdup(fc->values[i].value_name);
      values[i].nick = strdup(fc->values[i].value_nick);
      values[i].value = (int)fc->values[i].value;
    }
  } else {
    GEnumClass *ec = (GEnumClass *)klass;
    n_values = ec->n_values;
    values = calloc(n_values, sizeof(VipsxEnumValue));
    for (guint i = 0; i < n_values; i++) {
      values[i].name = strdup(ec->values[i].value_name);
      values[i].nick = strdup(ec->values[i].value_nick);
      values[i].value = ec->values[i].value;
    }
  }

  g_type_class_unref(klass);
  *count = (int)n_values;
  return values;
}

void vipsx_enum_values_free(VipsxEnumValue *values, int count) {
  for (int i = 0; i < count; i++) {
    free(values[i].name);
    free(values[i].nick);
  }
  free(values);
}

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

void vipsx_concurrency_set(int n) { vips_concurrency_set(n); }

int vipsx_concurrency_get(void) { return vips_concurrency_get(); }

void vipsx_image_unref(VipsImage *image) { g_object_unref(image); }

int vipsx_image_width(VipsImage *image) { return image->Xsize; }

int vipsx_image_height(VipsImage *image) { return image->Ysize; }

int vipsx_image_bands(VipsImage *image) { return image->Bands; }

// ---------------------------------------------------------------------------
// Image header accessors.
// ---------------------------------------------------------------------------

int vipsx_image_format(VipsImage *image) { return (int)image->BandFmt; }
int vipsx_image_interpretation(VipsImage *image) { return (int)image->Type; }
int vipsx_image_coding(VipsImage *image) { return (int)image->Coding; }
double vipsx_image_xres(VipsImage *image) { return image->Xres; }
double vipsx_image_yres(VipsImage *image) { return image->Yres; }
int vipsx_image_xoffset(VipsImage *image) { return image->Xoffset; }
int vipsx_image_yoffset(VipsImage *image) { return image->Yoffset; }
int vipsx_image_has_alpha(VipsImage *image) {
  return vips_image_hasalpha(image) ? 1 : 0;
}

// ---------------------------------------------------------------------------
// Image metadata.
//
// Reported through the same eighteen kinds as operation arguments, so Go has
// one type switch rather than two.
// ---------------------------------------------------------------------------

char **vipsx_image_fields(VipsImage *image, int *count) {
  char **fields = vips_image_get_fields(image);
  int n = 0;
  while (fields && fields[n])
    n++;
  *count = n;
  return fields;
}

int vipsx_image_has_field(VipsImage *image, const char *name) {
  return vips_image_get_typeof(image, name) != 0 ? 1 : 0;
}

int vipsx_image_get_kind(VipsImage *image, const char *name) {
  GType t = vips_image_get_typeof(image, name);
  if (t == 0)
    return VIPSX_KIND_UNKNOWN;
  return vipsx_kind_of_gtype(t);
}

int vipsx_image_get_int(VipsImage *image, const char *name, int *out) {
  return vips_image_get_int(image, name, out);
}

int vipsx_image_get_double(VipsImage *image, const char *name, double *out) {
  return vips_image_get_double(image, name, out);
}

char *vipsx_image_get_string(VipsImage *image, const char *name) {
  const char *s = NULL;
  if (vips_image_get_string(image, name, &s) != 0 || !s)
    return NULL;
  return strdup(s);
}

char *vipsx_image_get_as_string(VipsImage *image, const char *name) {
  char *s = NULL;
  if (vips_image_get_as_string(image, name, &s) != 0)
    return NULL;
  return s; // already g_malloc'd; freed with vipsx_free
}

void *vipsx_image_get_blob(VipsImage *image, const char *name, size_t *len) {
  const void *data = NULL;
  if (vips_image_get_blob(image, name, &data, len) != 0)
    return NULL;
  void *copy = malloc(*len);
  memcpy(copy, data, *len);
  return copy;
}

int vipsx_image_get_array_double(VipsImage *image, const char *name,
                                 double **out, int *n) {
  double *a = NULL;
  if (vips_image_get_array_double(image, name, &a, n) != 0)
    return -1;
  *out = malloc(*n * sizeof(double));
  memcpy(*out, a, *n * sizeof(double));
  return 0;
}

int vipsx_image_get_array_int(VipsImage *image, const char *name, int **out,
                              int *n) {
  int *a = NULL;
  if (vips_image_get_array_int(image, name, &a, n) != 0)
    return -1;
  *out = malloc(*n * sizeof(int));
  memcpy(*out, a, *n * sizeof(int));
  return 0;
}

void vipsx_image_set_int(VipsImage *image, const char *name, int value) {
  vips_image_set_int(image, name, value);
}

void vipsx_image_set_double(VipsImage *image, const char *name, double value) {
  vips_image_set_double(image, name, value);
}

void vipsx_image_set_string(VipsImage *image, const char *name,
                            const char *value) {
  vips_image_set_string(image, name, value);
}

void vipsx_image_set_blob(VipsImage *image, const char *name, const void *data,
                          size_t len) {
  void *copy = g_malloc(len);
  memcpy(copy, data, len);
  vips_image_set_blob(image, name, (VipsCallbackFn)g_free, copy, len);
}

int vipsx_image_remove(VipsImage *image, const char *name) {
  return vips_image_remove(image, name) ? 1 : 0;
}

// ---------------------------------------------------------------------------
// Interpolators, sources and targets.
// ---------------------------------------------------------------------------

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
  vips_target_end(target);

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

// ---------------------------------------------------------------------------
// Allocation counters.
// ---------------------------------------------------------------------------

void vipsx_cache_set_max(int max) { vips_cache_set_max(max); }
void vipsx_cache_set_max_mem(size_t bytes) { vips_cache_set_max_mem(bytes); }
int vipsx_cache_get_max(void) { return vips_cache_get_max(); }
int vipsx_cache_get_size(void) { return vips_cache_get_size(); }
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
