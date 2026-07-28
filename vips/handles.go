package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"runtime"
	"sync/atomic"
	"unsafe"
)

// handle is the shared body of the non-image GObjects an operation can take as
// an argument. They follow the same rule as Image: one reference, dropped by
// Close or by the collector.
type handle struct {
	ptr     unsafe.Pointer
	cleanup runtime.Cleanup
	closed  atomic.Bool
}

func (h *handle) init(p unsafe.Pointer) {
	h.ptr = p
	h.cleanup = runtime.AddCleanup(h, func(ptr unsafe.Pointer) {
		C.vipsx_object_unref(ptr)
	}, p)
}

func (h *handle) close() {
	if h == nil || !h.closed.CompareAndSwap(false, true) {
		return
	}
	h.cleanup.Stop()
	C.vipsx_object_unref(h.ptr)
	h.ptr = nil
	runtime.KeepAlive(h)
}

func (h *handle) cPointer() unsafe.Pointer { return h.ptr }

// Interpolate is a resampling method, taken by operations such as affine and
// mapim. Names come from libvips: "nearest", "bilinear", "bicubic", "lbb",
// "nohalo", "vsqbs".
type Interpolate struct{ handle }

// NewInterpolate looks up an interpolator by name.
func NewInterpolate(name string) (*Interpolate, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	p := C.vipsx_interpolate_new(cname)
	if p == nil {
		return nil, &Error{Op: "interpolate", Message: "no interpolator named " + name}
	}
	i := &Interpolate{}
	i.init(unsafe.Pointer(p))
	return i, nil
}

// Close releases the interpolator.
func (i *Interpolate) Close() { i.close() }

// Source is somewhere an image can be read from. Operations whose names end in
// _source take one.
type Source struct{ handle }

// NewSourceFromFile opens a file as a source.
func NewSourceFromFile(path string) (*Source, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	p := C.vipsx_source_new_from_file(cpath)
	if p == nil {
		return nil, &Error{Op: "source", Message: lastError()}
	}
	s := &Source{}
	s.init(unsafe.Pointer(p))
	return s, nil
}

// NewSourceFromBytes wraps a byte slice as a source. libvips takes its own
// copy, so the slice may be reused immediately.
func NewSourceFromBytes(data []byte) (*Source, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, &Error{Op: "source", Message: "empty buffer"}
	}
	p := C.vipsx_source_new_from_memory(unsafe.Pointer(&data[0]), C.size_t(len(data)))
	runtime.KeepAlive(data)
	if p == nil {
		return nil, &Error{Op: "source", Message: lastError()}
	}
	s := &Source{}
	s.init(unsafe.Pointer(p))
	return s, nil
}

// Close releases the source.
func (s *Source) Close() { s.close() }

// Target is somewhere an image can be written to. Operations whose names end in
// _target take one.
type Target struct {
	handle
	memory bool
}

// NewTargetToFile creates a target writing to a file.
func NewTargetToFile(path string) (*Target, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	p := C.vipsx_target_new_to_file(cpath)
	if p == nil {
		return nil, &Error{Op: "target", Message: lastError()}
	}
	t := &Target{}
	t.init(unsafe.Pointer(p))
	return t, nil
}

// NewTargetToMemory creates a target that accumulates in memory. Read the
// result with Bytes.
func NewTargetToMemory() (*Target, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	p := C.vipsx_target_new_to_memory()
	if p == nil {
		return nil, &Error{Op: "target", Message: lastError()}
	}
	t := &Target{memory: true}
	t.init(unsafe.Pointer(p))
	return t, nil
}

// Bytes takes the accumulated bytes from a memory target. It can only be called
// once, and only on a target made by NewTargetToMemory.
func (t *Target) Bytes() ([]byte, error) {
	defer runtime.KeepAlive(t)
	if !t.memory {
		return nil, &Error{Op: "target", Message: "not a memory target"}
	}
	var length C.size_t
	p := C.vipsx_target_steal((*C.VipsTarget)(t.ptr), &length)
	if p == nil {
		return nil, &Error{Op: "target", Message: "nothing written"}
	}
	defer C.vipsx_gfree(p)
	return C.GoBytes(p, C.int(length)), nil
}

// Close releases the target.
func (t *Target) Close() { t.close() }

// SetCacheMax bounds how many built operations libvips keeps for reuse. Zero
// disables the cache, which is what a leak check wants: a growing cache and a
// leak look the same from outside.
func SetCacheMax(n int) { C.vipsx_cache_set_max(C.int(n)) }

// SetCacheMaxMem bounds the operation cache by bytes rather than by count.
func SetCacheMaxMem(bytes uint64) { C.vipsx_cache_set_max_mem(C.size_t(bytes)) }

// CacheMax reports the current operation limit.
func CacheMax() int { return int(C.vipsx_cache_get_max()) }

// CacheSize reports how many operations are cached right now.
func CacheSize() int { return int(C.vipsx_cache_get_size()) }

// ClearCache drops every cached operation.
func ClearCache() { C.vipsx_cache_drop_all() }

// MemoryStats reports libvips' own allocation counters, which are what a leak
// check should watch: Go's heap says nothing about memory held on the C side.
type MemoryStats struct {
	Bytes     uint64 // currently allocated
	HighWater uint64 // peak since startup
	Allocs    int    // live allocations
	Files     int    // open file descriptors libvips is tracking
}

// Memory reports the current allocation counters.
func Memory() MemoryStats {
	return MemoryStats{
		Bytes:     uint64(C.vipsx_tracked_mem()),
		HighWater: uint64(C.vipsx_tracked_mem_highwater()),
		Allocs:    int(C.vipsx_tracked_allocs()),
		Files:     int(C.vipsx_tracked_files()),
	}
}
