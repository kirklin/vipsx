package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// unsupported reports that libvips is too old for a feature, naming what would
// be needed. The alternative — doing nothing and saying nothing — is the worst
// outcome for a hardening switch, since the caller believes it is protected.
func unsupported(op string, major, minor int) error {
	return &Error{
		Op: op,
		Message: fmt.Sprintf("needs libvips %d.%d or newer; this is %s",
			major, minor, Version()),
	}
}

// BlockUntrusted blocks every loader libvips marks untrusted.
//
// libvips carries a judgement about its own dependencies: a loader is untrusted
// when the code behind it is not one the libvips authors would put in front of
// an arbitrary upload. Blocking them costs the formats nobody asked for and
// removes the decoders with the worst history.
//
// This is process-wide and takes effect for operations built afterwards, so it
// belongs at start-up, before any request is served:
//
//	if err := vips.BlockUntrusted(true); err != nil {
//	    log.Fatal(err)
//	}
func BlockUntrusted(block bool) error {
	if err := Startup(); err != nil {
		return err
	}
	if C.vipsx_block_untrusted_set(cbool(block)) == 0 {
		return unsupported("block_untrusted", 8, 13)
	}
	return nil
}

// BlockOperation blocks or unblocks operations by class name, covering that
// class and everything below it.
//
// Names are libvips class names rather than operation nicknames — the thing
// Describe reports as a nickname is "jpegload", the class is
// "VipsForeignLoadJpeg". The hierarchy is the point: blocking "VipsForeignLoad"
// blocks every loader in one call.
//
// The useful shape is deny-all then allow-what-you-serve, which is what an
// image service actually wants:
//
//	vips.BlockOperation("VipsForeignLoad", true)       // nothing loads
//	vips.BlockOperation("VipsForeignLoadJpeg", false)  // except these
//	vips.BlockOperation("VipsForeignLoadPng", false)
//	vips.BlockOperation("VipsForeignLoadWebp", false)
//
// A blocked operation fails when it is built, so a caller sees an error rather
// than a decoded image.
func BlockOperation(name string, block bool) error {
	if err := Startup(); err != nil {
		return err
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	if C.vipsx_operation_block_set(cname, cbool(block)) == 0 {
		return unsupported("operation_block", 8, 13)
	}
	return nil
}

// SetPipeReadLimit bounds how much libvips will buffer from a source it cannot
// seek. A negative limit removes the bound.
//
// This matters exactly where vipsx is most useful. A file-backed source is read
// where the loader wants; an HTTP body is not seekable, so libvips buffers it
// to imitate one, and without a limit the size of that buffer is whatever the
// far end decides to send. libvips defaults to 1 GB, which is a limit but not
// one a request handler would choose.
//
//	vips.SetPipeReadLimit(64 << 20) // 64 MB per stream, then the load fails
func SetPipeReadLimit(bytes int64) error {
	if err := Startup(); err != nil {
		return err
	}
	if C.vipsx_pipe_read_limit_set(C.gint64(bytes)) == 0 {
		return unsupported("pipe_read_limit", 8, 13)
	}
	return nil
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
