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
// Close or by the collector, and read atomically so a Close racing a use is a
// defined *ClosedError rather than a read of freed memory.
type handle struct {
	ptr     atomic.Pointer[C.GObject]
	cleanup runtime.Cleanup
}

func (h *handle) init(p unsafe.Pointer) {
	cp := (*C.GObject)(p)
	h.ptr.Store(cp)
	h.cleanup = runtime.AddCleanup(h, func(ptr *C.GObject) {
		if shutdownDone.Load() {
			return
		}
		C.vipsx_object_unref(unsafe.Pointer(ptr))
	}, cp)
}

func (h *handle) close() {
	if h == nil {
		return
	}
	p := h.ptr.Swap(nil)
	if p == nil {
		return
	}
	h.cleanup.Stop()
	C.vipsx_object_unref(unsafe.Pointer(p))
	runtime.KeepAlive(h)
}

// live returns the wrapped object, or panics if the handle is closed. See
// (*Image).live for why this is a panic and not a NULL passed along.
func (h *handle) live(op string) unsafe.Pointer {
	if h == nil {
		panic(&ClosedError{Op: op})
	}
	p := h.ptr.Load()
	if p == nil {
		panic(&ClosedError{Op: op})
	}
	return unsafe.Pointer(p)
}

func (h *handle) cPointer() unsafe.Pointer {
	if h == nil {
		return nil
	}
	return unsafe.Pointer(h.ptr.Load())
}

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
type Source struct {
	handle
	// st is non-nil when the bytes come from an io.Reader. It lives on the
	// handle rather than only in the registry so Err keeps working after
	// Close; the registry entry itself lasts as long as the C object does.
	st *stream
}

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

// Close releases the source, and the reader behind it when there is one. Any
// image loaded from the source must be evaluated or closed first; a demand for
// bytes after this fails its operation.
func (s *Source) Close() {
	s.close()
	if s.st != nil {
		unregisterStream(s.st.id)
	}
}

// Err reports the first error the underlying reader returned, if any. libvips
// turns a failed read into an operation error of its own, which says that
// reading failed but not why; this says why. It keeps answering after Close.
func (s *Source) Err() error {
	if s.st == nil {
		return nil
	}
	return s.st.firstErr()
}

// Target is somewhere an image can be written to. Operations whose names end in
// _target take one.
type Target struct {
	handle
	memory bool
	// st is non-nil when the bytes go to an io.Writer. On the handle rather
	// than only in the registry so Err keeps working after Close.
	st *stream
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

// Bytes copies out what a memory target has accumulated so far. It only works
// on a target made by NewTargetToMemory.
//
// The bytes are copied rather than stolen, so calling this twice returns the
// same content twice rather than nothing the second time.
func (t *Target) Bytes() ([]byte, error) {
	p := t.live("Target.Bytes")
	defer runtime.KeepAlive(t)
	if !t.memory {
		return nil, &Error{Op: "target", Message: "not a memory target"}
	}
	var length C.size_t
	buf := C.vipsx_target_steal((*C.VipsTarget)(p), &length)
	if buf == nil {
		return nil, &Error{Op: "target", Message: "nothing written"}
	}
	defer C.vipsx_gfree(buf)
	return goBytes(buf, length), nil
}

// Close releases the target, and the writer behind it when there is one.
func (t *Target) Close() {
	t.close()
	if t.st != nil {
		unregisterStream(t.st.id)
	}
}

// Err reports the first error the underlying writer returned, if any. A save
// through a failing writer fails as a libvips error, which reports that writing
// failed but not why; this says why. It keeps answering after Close.
func (t *Target) Err() error {
	if t.st == nil {
		return nil
	}
	return t.st.firstErr()
}

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
