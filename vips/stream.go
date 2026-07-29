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
// The entry is removed at Close, deterministically, rather than when libvips
// lets go of the C object. Letting the object decide was tried and measured:
// evaluating an image drags internal operations into the libvips operation
// cache, the cache holds the pipeline, the pipeline holds the source, and the
// reader stays pinned until an eviction that may never come. Close severing
// the registry frees the reader on the spot; a callback arriving afterwards
// finds nothing and fails the operation cleanly. The C object also carries a
// weak reference as a backstop, so a handle that is never closed still has its
// entry removed when the collector and libvips are both done with it.
var (
	streamsMu sync.RWMutex
	streams   = map[uint64]*stream{}
	streamID  atomic.Uint64
)

type stream struct {
	id     uint64
	reader io.Reader
	seeker io.Seeker
	writer io.Writer

	// err is written from whichever thread libvips calls back on and read from
	// whatever goroutine asks Err, so it takes a lock rather than a comment
	// about who may touch it when.
	mu  sync.Mutex
	err error // the first failure, kept for Err to report
}

// setErr keeps the first error. Later ones are usually consequences of it.
func (s *stream) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *stream) firstErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func registerStream(s *stream) uint64 {
	s.id = streamID.Add(1)
	streamsMu.Lock()
	streams[s.id] = s
	streamsMu.Unlock()
	return s.id
}

// OpenStreams reports how many reader- or writer-backed sources and targets are
// still registered. It should return to zero once everything is closed; a
// number that only grows means Close is being skipped somewhere, and each
// missed one pins a reader or writer until the collector gets to the handle.
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

func unregisterStream(id uint64) {
	streamsMu.Lock()
	delete(streams, id)
	streamsMu.Unlock()
}

//export vipsxGoRead
func vipsxGoRead(id C.guint64, buffer unsafe.Pointer, length C.gint64) C.gint64 {
	s := lookupStream(uint64(id))
	if s == nil || s.reader == nil || length <= 0 {
		return -1
	}

	n, err := s.reader.Read(unsafe.Slice((*byte)(buffer), int(length)))
	if n > 0 {
		// A short read is not an error to libvips; it asks again. The error
		// still has to be kept now: a reader that hands over its last bytes
		// alongside the failure is not obliged to repeat itself when asked
		// again, and the reason would otherwise be gone.
		if err != nil && err != io.EOF {
			s.setErr(err)
		}
		return C.gint64(n)
	}
	if err == io.EOF {
		return 0
	}
	if err != nil {
		s.setErr(err)
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
		s.setErr(err)
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
		s.setErr(err)
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
			s.setErr(err)
			return -1
		}
	}
	return 0
}

// vipsxGoStreamGone runs when the C object dies, from the weak reference the
// constructor took. Close has usually unregistered the entry already and this
// finds nothing to do; it exists for the handle that is never closed, whose
// entry would otherwise outlive every reference the caller has.
//
//export vipsxGoStreamGone
func vipsxGoStreamGone(id C.guint64) {
	unregisterStream(uint64(id))
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
// Close releases r immediately, so it must come after every image loaded from
// the source has been evaluated or closed — save first, then close. This is
// one place a reader-backed source is stricter than a file-backed one: a file
// stays readable through libvips' own reference after the handle closes, while
// a demand for bytes after this Close fails the operation. It fails cleanly,
// with an error rather than anything worse, but the error is libvips' generic
// read failure and does not name the cause. r itself is never closed here;
// that stays with whoever opened it.
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
		// No object was made, so no weak reference will ever fire.
		unregisterStream(id)
		return nil, &Error{Op: "source", Message: lastError()}
	}

	src := &Source{st: st}
	src.init(unsafe.Pointer(p))
	return src, nil
}

// NewTargetToWriter writes an image to w as libvips produces it.
//
// If w has a Flush method returning an error it is called when libvips
// finishes, so a bufio.Writer does not need flushing separately.
//
// A save through a failing writer fails as a libvips error, which says that
// writing failed but not why. Err says why, and keeps saying it after Close,
// so checking it after a failed save is enough — deferring Close does not
// discard the reason.
func NewTargetToWriter(w io.Writer) (*Target, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	if w == nil {
		return nil, &Error{Op: "target", Message: "nil writer"}
	}

	st := &stream{writer: w}
	id := registerStream(st)

	p := C.vipsx_target_custom_new(C.guint64(id))
	if p == nil {
		unregisterStream(id)
		return nil, &Error{Op: "target", Message: lastError()}
	}

	t := &Target{st: st}
	t.init(unsafe.Pointer(p))
	return t, nil
}
