package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"io"
	"sync"
	"sync/atomic"
	"unsafe"
)

// libvips pulls bytes by calling back, and the callback arrives on the C side
// with no way to carry a Go pointer: cgo forbids storing one there. So each
// stream is registered under a number, and only the number crosses over.
//
// The registry also decides lifetime. An entry lives until the Source or Target
// is closed, which is why closing one is not optional when a reader or writer
// is involved — the entry, and the reader it holds, would otherwise outlive
// every reference the caller has.
var (
	streamsMu sync.RWMutex
	streams   = map[uint64]*stream{}
	streamID  atomic.Uint64
)

type stream struct {
	reader io.Reader
	seeker io.Seeker
	writer io.Writer
	err    error // the first failure, kept so Close can report it
}

func registerStream(s *stream) uint64 {
	id := streamID.Add(1)
	streamsMu.Lock()
	streams[id] = s
	streamsMu.Unlock()
	return id
}

// OpenStreams reports how many reader- or writer-backed sources and targets are
// still registered. It should return to zero once everything is closed; a
// number that only grows means Close is being skipped somewhere, and each
// missed one pins a reader or writer for the life of the process.
func OpenStreams() int {
	streamsMu.RLock()
	defer streamsMu.RUnlock()
	return len(streams)
}

func lookupStream(id uint64) *stream {
	streamsMu.RLock()
	defer streamsMu.RUnlock()
	return streams[id]
}

func unregisterStream(id uint64) *stream {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	s := streams[id]
	delete(streams, id)
	return s
}

//export vipsxGoRead
func vipsxGoRead(id C.guint64, buffer unsafe.Pointer, length C.gint64) C.gint64 {
	s := lookupStream(uint64(id))
	if s == nil || s.reader == nil || length <= 0 {
		return -1
	}

	n, err := s.reader.Read(unsafe.Slice((*byte)(buffer), int(length)))
	if n > 0 {
		// A short read is not an error to libvips; it asks again.
		return C.gint64(n)
	}
	if err == io.EOF {
		return 0
	}
	if err != nil {
		if s.err == nil {
			s.err = err
		}
		return -1
	}
	return 0
}

//export vipsxGoSeek
func vipsxGoSeek(id C.guint64, offset C.gint64, whence C.int) C.gint64 {
	s := lookupStream(uint64(id))
	if s == nil || s.seeker == nil {
		return -1
	}
	pos, err := s.seeker.Seek(int64(offset), int(whence))
	if err != nil {
		if s.err == nil {
			s.err = err
		}
		return -1
	}
	return C.gint64(pos)
}

//export vipsxGoWrite
func vipsxGoWrite(id C.guint64, data unsafe.Pointer, length C.gint64) C.gint64 {
	s := lookupStream(uint64(id))
	if s == nil || s.writer == nil {
		return -1
	}
	n, err := s.writer.Write(unsafe.Slice((*byte)(data), int(length)))
	if err != nil {
		if s.err == nil {
			s.err = err
		}
		return -1
	}
	return C.gint64(n)
}

//export vipsxGoEnd
func vipsxGoEnd(id C.guint64) C.int {
	s := lookupStream(uint64(id))
	if s == nil {
		return -1
	}
	if f, ok := s.writer.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			if s.err == nil {
				s.err = err
			}
			return -1
		}
	}
	return 0
}

// NewSourceFromReader reads an image from r as libvips asks for it, without
// holding the whole thing in memory.
//
// When r is also an io.Seeker the source is told so, and libvips can jump
// around the file the way it does with a real one. When it is not — an HTTP
// body, a pipe — libvips takes its sequential path instead and buffers what it
// needs. Both work; the seekable one works for more formats, since a few
// loaders cannot operate without seeking.
//
// Close releases the source and forgets r. It is not optional: until it is
// called, the registry holds a reference to r for libvips to call back into.
func NewSourceFromReader(r io.Reader) (*Source, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, &Error{Op: "source", Message: "nil reader"}
	}

	st := &stream{reader: r}
	seekable := 0
	if s, ok := r.(io.Seeker); ok {
		st.seeker = s
		seekable = 1
	}
	id := registerStream(st)

	p := C.vipsx_source_custom_new(C.guint64(id), C.int(seekable))
	if p == nil {
		unregisterStream(id)
		return nil, &Error{Op: "source", Message: lastError()}
	}

	src := &Source{streamID: id}
	src.init(unsafe.Pointer(p))
	return src, nil
}

// NewTargetToWriter writes an image to w as libvips produces it.
//
// If w has a Flush method it is called when libvips finishes, so a
// bufio.Writer does not need flushing separately.
//
// Close releases the target and forgets w, and reports the first error the
// writer returned. Ignoring it discards write failures.
func NewTargetToWriter(w io.Writer) (*Target, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if w == nil {
		return nil, &Error{Op: "target", Message: "nil writer"}
	}

	id := registerStream(&stream{writer: w})

	p := C.vipsx_target_custom_new(C.guint64(id))
	if p == nil {
		unregisterStream(id)
		return nil, &Error{Op: "target", Message: lastError()}
	}

	t := &Target{streamID: id}
	t.init(unsafe.Pointer(p))
	return t, nil
}
