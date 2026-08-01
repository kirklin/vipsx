// Allocation that reports failure instead of handing back a NULL for someone
// else to memcpy into.
//
// Every allocation in this package used to be an unchecked malloc or strdup
// followed immediately by a write. That is fine until it is not, and when it is
// not the symptom is a segfault inside libvips with no indication that the real
// problem was memory. These stay in the malloc family on purpose: what they
// return is released with free(), on both sides of the cgo boundary.

#include "vipsx.h"

void *vipsx_alloc0(size_t n) {
  // A zero-length request still gets a real pointer. Callers memcpy into the
  // result without checking the length first, and a NULL here would be read as
  // failure by everything above.
  if (n == 0)
    n = 1;
  return calloc(1, n);
}

char *vipsx_dup(const char *s) {
  if (!s)
    s = "";
  size_t n = strlen(s) + 1;
  char *copy = malloc(n);
  if (copy)
    memcpy(copy, s, n);
  return copy;
}
