// The generic call path: one type switch and one entry point that together
// reach every libvips operation.
//
// Nothing here is per-operation, and nothing skips an argument because its value
// happens to be zero.

#include "vipsx.h"

// The type switch. Eighteen cases, and that is the whole of it.
//
// Ordering matters: the specific VIPS boxed and object types must be tested
// before the general G_TYPE_OBJECT fallback, or every image would classify as
// a plain object.
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

// Setting one argument.
//
// Every supplied argument is set. There is no "skip if the value is zero"
// branch anywhere in this file, which is what makes an explicit 0, false or ""
// expressible. Absence is represented by not appearing in the argument list.
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

// Reading one output.
//
// Reference rules follow the libvips binding guide: g_value_dup_object takes
// the reference we hand to the caller, and everything heap-allocated here is
// released by vipsx_out_clear once Go has copied it.
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
