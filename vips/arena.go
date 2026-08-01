package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import "unsafe"

// arena collects the C allocations made while marshalling one call so they can
// be released together afterwards.
//
// Everything handed to C is copied into C memory rather than pointing at Go
// memory. The copies are small next to the cost of decoding an image, and they
// keep the whole call clear of the cgo pointer-passing rules.
type arena struct {
	ptrs []unsafe.Pointer
}

func (a *arena) track(p unsafe.Pointer) unsafe.Pointer {
	a.ptrs = append(a.ptrs, p)
	return p
}

func (a *arena) cstring(s string) *C.char {
	p := C.CString(s)
	a.track(unsafe.Pointer(p))
	return p
}

func (a *arena) alloc(n int) unsafe.Pointer {
	if n < 1 {
		n = 1 // never hand libvips a null base pointer
	}
	p := C.malloc(C.size_t(n))
	if p == nil {
		// The same exhaustion makes C.CString panic, so a panic keeps the two
		// failure modes alike. Unchecked, the nil would surface later as a
		// write through a bad pointer with nothing pointing back here.
		panic("vips: out of memory marshalling arguments")
	}
	return a.track(p)
}

func (a *arena) ints(xs []int) (unsafe.Pointer, C.size_t) {
	p := a.alloc(len(xs) * C.sizeof_int)
	dst := unsafe.Slice((*C.int)(p), max(len(xs), 1))
	for i, x := range xs {
		dst[i] = C.int(x)
	}
	return p, C.size_t(len(xs))
}

func (a *arena) doubles(xs []float64) (unsafe.Pointer, C.size_t) {
	p := a.alloc(len(xs) * C.sizeof_double)
	dst := unsafe.Slice((*C.double)(p), max(len(xs), 1))
	for i, x := range xs {
		dst[i] = C.double(x)
	}
	return p, C.size_t(len(xs))
}

func (a *arena) pointers(xs []unsafe.Pointer) (unsafe.Pointer, C.size_t) {
	p := a.alloc(len(xs) * int(unsafe.Sizeof(uintptr(0))))
	dst := unsafe.Slice((*unsafe.Pointer)(p), max(len(xs), 1))
	copy(dst, xs)
	return p, C.size_t(len(xs))
}

func (a *arena) bytes(b []byte) (unsafe.Pointer, C.size_t) {
	p := a.alloc(len(b))
	if len(b) > 0 {
		copy(unsafe.Slice((*byte)(p), len(b)), b)
	}
	return p, C.size_t(len(b))
}

func (a *arena) free() {
	for _, p := range a.ptrs {
		C.free(p)
	}
	a.ptrs = nil
}
