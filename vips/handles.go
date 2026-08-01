package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"os"
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
	// file is non-nil when the source reads a descriptor. libvips neither dups
	// nor closes it, so holding the *os.File here is what stops a collector
	// closing the descriptor out from under libvips.
	file *os.File
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

// NewSourceFromOpenFile reads an image through an already-open file's
// descriptor.
//
// This is the cheapest way in for something that is already a file. The
// io.Reader path crosses into Go for every read — a cgo call, a registry
// lookup and a callback each time — while this one leaves libvips reading the
// descriptor directly.
//
// libvips neither duplicates nor closes the descriptor, so f must stay open for
// as long as the source is used, and closing it stays the caller's job. The
// source holds a reference to f so a collector cannot close it early; that is
// the one part the caller does not have to think about.
//
//	f, err := os.Open("photo.jpg")
//	if err != nil { return err }
//	defer f.Close()
//
//	src, err := vips.NewSourceFromOpenFile(f)
//	if err != nil { return err }
//	defer src.Close()
func NewSourceFromOpenFile(f *os.File) (*Source, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if f == nil {
		return nil, &Error{Op: "source", Message: "nil file"}
	}
	p := C.vipsx_source_new_from_descriptor(C.int(f.Fd()))
	if p == nil {
		return nil, &Error{Op: "source", Message: lastError()}
	}
	s := &Source{file: f}
	s.init(unsafe.Pointer(p))
	return s, nil
}

// Sniff copies the first n bytes of a source without consuming them, so the
// loader that runs next still reads from the start.
//
// This is where a format check of your own goes — a magic-number allowlist
// stricter than the loaders libvips is willing to try. It reports an error when
// the source is shorter than n.
func (s *Source) Sniff(n int) ([]byte, error) {
	p := s.live("Source.Sniff")
	defer runtime.KeepAlive(s)
	if n <= 0 {
		return nil, &Error{Op: "source", Message: "sniff length must be positive"}
	}
	data := C.vipsx_source_sniff((*C.VipsSource)(p), C.size_t(n))
	if data == nil {
		return nil, &Error{Op: "source",
			Message: fmt.Sprintf("fewer than %d bytes available", n)}
	}
	return goBytes(data, C.size_t(n)), nil
}

// Length reports how many bytes the source holds, or -1 when it cannot say —
// an unseekable stream does not know until it has read to the end.
//
// A cheap first gate for a size limit, before any decoding happens.
func (s *Source) Length() int64 {
	p := s.live("Source.Length")
	defer runtime.KeepAlive(s)
	return int64(C.vipsx_source_length((*C.VipsSource)(p)))
}

// Minimise releases what the source is holding open, keeping it usable: the
// next read reopens whatever it needs.
//
// For a process juggling more sources than it has descriptors.
func (s *Source) Minimise() {
	p := s.live("Source.Minimise")
	defer runtime.KeepAlive(s)
	C.vipsx_source_minimise((*C.VipsSource)(p))
}

// Unminimise undoes Minimise, reopening ahead of a read rather than during one.
func (s *Source) Unminimise() {
	p := s.live("Source.Unminimise")
	defer runtime.KeepAlive(s)
	C.vipsx_source_unminimise((*C.VipsSource)(p))
}

// Close releases the source, and the reader behind it when there is one.
//
// Any image loaded from the source must be evaluated or closed first: a demand
// for bytes after this fails its operation, and Err then reports
// ErrStreamClosed. CopyMemory is the way to close early — it takes the pixels,
// after which the reader is no longer needed.
func (s *Source) Close() {
	s.close()
	if s.st != nil {
		s.st.release()
	}
}

// Err reports why reading failed, when it did.
//
// libvips turns a failed read into an operation error of its own, which says
// that reading failed and nothing about the cause. This is the cause: whatever
// the reader returned, or ErrStreamClosed if the source was closed while an
// image still needed it. It keeps answering after Close.
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
	// file is non-nil when the target writes to a descriptor; see Source.file.
	file *os.File
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

// NewTargetToOpenFile writes an image through an already-open file's
// descriptor. See NewSourceFromOpenFile for what libvips does and does not do
// with it.
func NewTargetToOpenFile(f *os.File) (*Target, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if f == nil {
		return nil, &Error{Op: "target", Message: "nil file"}
	}
	p := C.vipsx_target_new_to_descriptor(C.int(f.Fd()))
	if p == nil {
		return nil, &Error{Op: "target", Message: lastError()}
	}
	t := &Target{file: f}
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
		t.st.release()
	}
}

// Err reports why writing failed, when it did.
//
// A save through a failing writer fails as a libvips error, which reports that
// writing failed and nothing about the cause. This is the cause: whatever the
// writer returned, or ErrStreamClosed if the target was closed while a save
// still needed it. It keeps answering after Close.
//
// A target closed before a save even starts does not reach here: it is caught
// by the argument check, which reports it directly. Sources are the ones that
// can go away underneath an image, because an image is evaluated later.
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

// SetCacheMaxFiles bounds the operation cache by how many file descriptors the
// cached operations are holding open.
//
// This is the third of the cache's three limits, and the one most likely to be
// hit without warning: a process well under both the operation count and the
// byte ceiling can still run out of descriptors, because a cached loader keeps
// its file open. The symptom is unrelated parts of the program failing to open
// anything.
func SetCacheMaxFiles(n int) { C.vipsx_cache_set_max_files(C.int(n)) }

// CacheMaxFiles reports the descriptor limit.
func CacheMaxFiles() int { return int(C.vipsx_cache_get_max_files()) }

// CacheMaxMem reports the byte limit.
func CacheMaxMem() uint64 { return uint64(C.vipsx_cache_get_max_mem()) }

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
