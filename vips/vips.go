// Package vips is a Go binding for libvips built on runtime introspection.
//
// libvips describes its own operations through the GObject type system, so this
// package does not wrap operations one at a time. A single generic call path
// plus one eighteen-case type switch reaches every operation the installed
// libvips provides, including ones added after this package was written.
//
// The design has one property worth stating plainly: an argument that is
// supplied is always sent. There is no sentinel value and no "skip if zero"
// branch, so vips.In("band", 0) sets band to 0 rather than silently falling
// back to the libvips default.
package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	initOnce sync.Once
	initErr  error

	// shutdownDone is read by every collector cleanup. Shutdown frees the
	// world libvips lives in, and the collector is free to run a cleanup
	// afterwards; unreffing then is a use-after-free rather than tidiness.
	shutdownDone atomic.Bool
)

func init() { Startup() }

// Startup initialises libvips. It is called automatically on package load and
// is safe to call again; only the first call has an effect.
func Startup() error {
	initOnce.Do(func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		name := C.CString("vipsx")
		defer C.free(unsafe.Pointer(name))

		if C.vipsx_init(name) != 0 {
			initErr = fmt.Errorf("vips: init failed: %s", lastError())
		}
	})
	return initErr
}

// Shutdown releases libvips resources. Calling any other function afterwards is
// undefined; this exists for leak checking under valgrind and ASan.
//
// Handles still alive at this point are not the caller's problem: the collector
// may run their cleanups long after this returns, and those check the same flag
// set here rather than unreffing into freed memory.
func Shutdown() {
	shutdownDone.Store(true)
	C.vipsx_shutdown()
}

// goBytes copies a C buffer into Go memory.
//
// Not C.GoBytes, whose length is a C.int: a saved buffer can exceed 2 GB — a
// large TIFF manages it — and the conversion would silently go negative.
func goBytes(p unsafe.Pointer, n C.size_t) []byte {
	if p == nil || n == 0 {
		return nil
	}
	out := make([]byte, int(n))
	copy(out, unsafe.Slice((*byte)(p), int(n)))
	return out
}

// Version reports the libvips version this process is linked against. This is
// the version actually loaded at runtime, not one baked in at build time.
func Version() string { return C.GoString(C.vipsx_version_string()) }

// VersionParts reports major, minor and micro separately, for comparisons.
func VersionParts() (major, minor, micro int) {
	return int(C.vipsx_version(0)), int(C.vipsx_version(1)), int(C.vipsx_version(2))
}

// SetConcurrency sets how many worker threads libvips uses per operation.
//
// Beyond throughput, this affects reproducibility. Operations that reduce over
// a whole image — stats reporting where the maximum sits, for one — choose
// between equally correct answers according to which thread reached the tie
// first. Pinning concurrency to 1 makes those answers repeatable.
func SetConcurrency(n int) { C.vipsx_concurrency_set(C.int(n)) }

// Concurrency reports the current worker thread count.
func Concurrency() int { return int(C.vipsx_concurrency_get()) }

// lastError drains the libvips error buffer.
func lastError() string {
	cstr := C.vipsx_error_buffer_copy()
	defer C.free(unsafe.Pointer(cstr))
	return C.GoString(cstr)
}

var (
	// errorIsolation serialises calls so a failure's message is its own. See
	// SetErrorIsolation.
	errorIsolation atomic.Bool
	errorIsolMu    sync.Mutex
)

// SetErrorIsolation trades throughput for exact error attribution.
//
// libvips keeps one error buffer for the whole process, not one per thread or
// per call. Two operations failing at the same time append to the same buffer,
// so a message can arrive carrying another goroutine's text, or another
// goroutine's alone. Draining it is atomic here, which is why nothing is ever
// lost, but atomicity cannot say which failure a line came from.
//
// With isolation on, one operation runs at a time, so a failure's message can
// only be its own. That serialises every call, successful ones included, and is
// meant for reproducing a problem rather than for serving traffic. It is off by
// default.
//
// The limitation is libvips', not this package's: pyvips and govips read the
// same global buffer the same way.
func SetErrorIsolation(on bool) { errorIsolation.Store(on) }

// ErrorIsolation reports whether calls are being serialised for attribution.
func ErrorIsolation() bool { return errorIsolation.Load() }

// Error is returned when libvips rejects a call. Op names the operation that
// failed so the message stays useful once calls are nested.
//
// Message comes from libvips' process-wide error buffer. Under concurrency it
// can carry text from another failure that happened at the same moment; see
// SetErrorIsolation.
type Error struct {
	Op      string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("vips: %s failed", e.Op)
	}
	return fmt.Sprintf("vips: %s: %s", e.Op, e.Message)
}
