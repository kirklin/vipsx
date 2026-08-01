// Asking libvips what it can do: which operations exist, what arguments they
// take, and what the enum types behind those arguments contain.

#include "vipsx.h"

// Introspection: what operations exist, and what arguments do they take.
typedef struct {
  VipsxArgSpec *args;
  int count;
  int max;
  int failed; // an allocation did not come back
} CollectArgs;

// Count first, allocate exactly. There used to be a fixed ceiling of 64
// arguments here, quietly dropping anything past it — the widest operation in
// libvips 8.18 takes 31, so it never fired, but a cap that silently hides an
// argument is the same failure this package refuses everywhere else: Call would
// report "no argument q" for something that was simply not looked at.
static void *vipsx_count_arg(VipsObject *object, GParamSpec *pspec,
                             VipsArgumentClass *argument_class,
                             VipsArgumentInstance *argument_instance, void *a,
                             void *b) {
  (void)object;
  (void)pspec;
  (void)argument_class;
  (void)argument_instance;
  (void)b;
  (*(int *)a)++;
  return NULL;
}

static void *vipsx_collect_arg(VipsObject *object, GParamSpec *pspec,
                               VipsArgumentClass *argument_class,
                               VipsArgumentInstance *argument_instance, void *a,
                               void *b) {
  CollectArgs *data = (CollectArgs *)a;
  if (data->count >= data->max || data->failed)
    return NULL;

  VipsxArgSpec *spec = &data->args[data->count];
  GType type = G_PARAM_SPEC_VALUE_TYPE(pspec);

  spec->name = vipsx_dup(g_param_spec_get_name(pspec));
  const char *blurb = g_param_spec_get_blurb(pspec);
  spec->blurb = vipsx_dup(blurb ? blurb : "");
  spec->kind = vipsx_kind_of_gtype(type);
  spec->type_name = vipsx_dup(g_type_name(type));
  if (!spec->name || !spec->blurb || !spec->type_name) {
    data->failed = 1;
    return NULL;
  }

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
    spec->s_default = vipsx_dup(d ? d : "");
    if (!spec->s_default) {
      data->failed = 1;
      return NULL;
    }
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
  if (argument_class->flags & VIPS_ARGUMENT_MODIFY)
    flags |= VIPSX_FLAG_MODIFY;
  spec->flags = flags;

  data->count++;
  return NULL;
}

VipsxOpSpec *vipsx_op_spec(const char *operation) {
  VipsOperation *op = vips_operation_new(operation);
  if (!op)
    return NULL;

  VipsxOpSpec *spec = vipsx_alloc0(sizeof(VipsxOpSpec));
  if (!spec) {
    g_object_unref(op);
    return NULL;
  }

  VipsObjectClass *klass = VIPS_OBJECT_GET_CLASS(op);
  spec->name = vipsx_dup(operation);
  spec->description = vipsx_dup(klass->description ? klass->description : "");

  int n_args = 0;
  vips_argument_map(VIPS_OBJECT(op), vipsx_count_arg, &n_args, NULL);

  spec->args = vipsx_alloc0((size_t)n_args * sizeof(VipsxArgSpec));
  if (!spec->name || !spec->description || !spec->args) {
    vipsx_op_spec_free(spec);
    g_object_unref(op);
    return NULL;
  }

  CollectArgs data = {.args = spec->args, .count = 0, .max = n_args, .failed = 0};
  vips_argument_map(VIPS_OBJECT(op), vipsx_collect_arg, &data, NULL);
  spec->n_args = data.count;

  g_object_unref(op);

  if (data.failed) {
    vipsx_op_spec_free(spec);
    return NULL;
  }
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
  int failed;
} CollectOps;

// Grow rather than cap. The ceiling here was 2000 against 330 operations in
// libvips 8.18, so it never fired either — but a walk that silently stops is
// not something the caller can notice, and Operations() is what the coverage
// tests measure against.
static int vipsx_ops_room(CollectOps *data) {
  if (data->count < data->max)
    return 1;
  int want = data->max ? data->max * 2 : 64;
  char **grown = realloc(data->names, (size_t)want * sizeof(char *));
  if (!grown) {
    data->failed = 1;
    return 0;
  }
  data->names = grown;
  data->max = want;
  return 1;
}

static void vipsx_collect_ops(GType type, CollectOps *data) {
  if (data->failed)
    return;

  if (!G_TYPE_IS_ABSTRACT(type)) {
    VipsObjectClass *klass = VIPS_OBJECT_CLASS(g_type_class_ref(type));
    if (klass && klass->nickname) {
      VipsOperation *op = VIPS_OPERATION(g_object_new(type, NULL));
      int deprecated = 0;
      if (op) {
        deprecated =
            (vips_operation_get_flags(op) & VIPS_OPERATION_DEPRECATED) != 0;
        g_object_unref(op);
      }
      if (!deprecated && vipsx_ops_room(data)) {
        char *nick = vipsx_dup(klass->nickname);
        if (nick)
          data->names[data->count++] = nick;
        else
          data->failed = 1;
      }
    }
    g_type_class_unref(klass);
  }

  guint n_children = 0;
  GType *children = g_type_children(type, &n_children);
  for (guint i = 0; i < n_children && !data->failed; i++)
    vipsx_collect_ops(children[i], data);
  g_free(children);
}

char **vipsx_list_operations(int *count) {
  CollectOps data = {.names = NULL, .count = 0, .max = 0, .failed = 0};
  vipsx_collect_ops(VIPS_TYPE_OPERATION, &data);
  if (data.failed) {
    vipsx_strv_free(data.names, data.count);
    *count = 0;
    return NULL;
  }
  *count = data.count;
  return data.names;
}

void vipsx_strv_free(char **strv, int count) {
  if (!strv)
    return;
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
  int failed = 0;

  if (G_TYPE_IS_FLAGS(type)) {
    GFlagsClass *fc = (GFlagsClass *)klass;
    n_values = fc->n_values;
    values = vipsx_alloc0((size_t)n_values * sizeof(VipsxEnumValue));
    for (guint i = 0; values && i < n_values; i++) {
      values[i].name = vipsx_dup(fc->values[i].value_name);
      values[i].nick = vipsx_dup(fc->values[i].value_nick);
      values[i].value = (int)fc->values[i].value;
      failed |= !values[i].name || !values[i].nick;
    }
  } else {
    GEnumClass *ec = (GEnumClass *)klass;
    n_values = ec->n_values;
    values = vipsx_alloc0((size_t)n_values * sizeof(VipsxEnumValue));
    for (guint i = 0; values && i < n_values; i++) {
      values[i].name = vipsx_dup(ec->values[i].value_name);
      values[i].nick = vipsx_dup(ec->values[i].value_nick);
      values[i].value = ec->values[i].value;
      failed |= !values[i].name || !values[i].nick;
    }
  }

  g_type_class_unref(klass);

  if (!values || failed) {
    vipsx_enum_values_free(values, (int)n_values);
    *count = 0;
    return NULL;
  }
  *count = (int)n_values;
  return values;
}

void vipsx_enum_values_free(VipsxEnumValue *values, int count) {
  if (!values)
    return;
  for (int i = 0; i < count; i++) {
    free(values[i].name);
    free(values[i].nick);
  }
  free(values);
}
