// Asking libvips what it can do: which operations exist, what arguments they
// take, and what the enum types behind those arguments contain.

#include "vipsx.h"

// Introspection: what operations exist, and what arguments do they take.
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
