package soak

/*
#include <stdlib.h>
*/
import "C"

import "unsafe"

// leakOneBlock loses one kibibyte on purpose.
//
// It exists so the leak checker can be proven to work. cgo is not allowed in a
// _test.go file, which is the only reason this lives outside the test that
// calls it. Nothing else in this package uses it.
//
//go:noinline
func leakOneBlock() {
	p := C.malloc(1024)
	if p == nil {
		panic("malloc failed")
	}
	// Write to it so it cannot be optimised away, then clobber the only
	// pointer. Go's heap and stacks are registered as leak-checker roots, so a
	// stale copy left behind would make the block count as reachable.
	*(*byte)(unsafe.Pointer(p)) = 1
	p = nil
	_ = p
}
