package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import "sync/atomic"

// LogLevel mirrors GLib's log levels, which is what libvips writes through.
type LogLevel int

// The levels GLib defines. They are flags rather than an ordered scale, so a
// message carries exactly one of these bits.
const (
	LogError    LogLevel = 1 << 2 // always fatal to GLib
	LogCritical LogLevel = 1 << 3
	LogWarning  LogLevel = 1 << 4
	LogMessage  LogLevel = 1 << 5
	LogInfo     LogLevel = 1 << 6
	LogDebug    LogLevel = 1 << 7
)

func (l LogLevel) String() string {
	switch {
	case l&LogError != 0:
		return "error"
	case l&LogCritical != 0:
		return "critical"
	case l&LogWarning != 0:
		return "warning"
	case l&LogMessage != 0:
		return "message"
	case l&LogInfo != 0:
		return "info"
	case l&LogDebug != 0:
		return "debug"
	default:
		return "unknown"
	}
}

// logSink holds the installed handler. A func cannot go in an atomic.Pointer
// directly, so it travels in a struct.
type logSink struct {
	fn func(domain string, level LogLevel, message string)
}

var logHandler atomic.Pointer[logSink]

// SetLogHandler routes libvips and GLib diagnostics to fn.
//
// libvips complains through GLib, whose default handler writes to stderr. In a
// service that is the one place the message will not be found: no request id,
// no level, and nothing an aggregator can read. This puts those lines wherever
// the rest of the program's logs go.
//
//	vips.SetLogHandler(func(domain string, level vips.LogLevel, msg string) {
//	    slog.Warn("libvips", "domain", domain, "level", level.String(), "msg", msg)
//	})
//
// Passing nil restores GLib's default handler — GLib's, not whichever handler
// another library may have installed before this one: GLib does not expose the
// data pointer an earlier handler was registered with, so putting it back
// faithfully is not possible from here.
//
// Two things to know before installing one. It is process-wide, because GLib's
// handler is: a program using another GLib library will see that library's
// messages here too, which is usually what was wanted and occasionally a
// surprise. And fn runs on whatever thread produced the message, including
// libvips worker threads, so it has to be safe to call concurrently and should
// not call back into this package.
func SetLogHandler(fn func(domain string, level LogLevel, message string)) {
	if fn == nil {
		logHandler.Store(nil)
		C.vipsx_log_capture(0)
		return
	}
	logHandler.Store(&logSink{fn: fn})
	C.vipsx_log_capture(1)
}

//export vipsxGoLog
func vipsxGoLog(domain *C.char, level C.int, message *C.char) {
	sink := logHandler.Load()
	if sink == nil || sink.fn == nil {
		return
	}
	sink.fn(C.GoString(domain), LogLevel(level), C.GoString(message))
}
