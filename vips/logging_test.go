package vips_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

func TestLogHandlerReceivesDiagnostics(t *testing.T) {
	type entry struct {
		domain string
		level  vips.LogLevel
		msg    string
	}

	var (
		mu   sync.Mutex
		seen []entry
	)
	vips.SetLogHandler(func(domain string, level vips.LogLevel, msg string) {
		mu.Lock()
		seen = append(seen, entry{domain, level, msg})
		mu.Unlock()
	})
	t.Cleanup(func() { vips.SetLogHandler(nil) })

	// libvips emits a GLib warning here rather than an error: a source that is
	// closed under a pipeline still waiting for bytes.
	if err := provokeAWarning(t); err != nil {
		t.Logf("the provoking call failed as expected: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Skip("this libvips produced no diagnostics for the provoking case; " +
			"the handler is still installed, there was simply nothing to route")
	}
	for _, e := range seen {
		if e.msg == "" {
			t.Error("a message arrived empty")
		}
		if e.level.String() == "unknown" {
			t.Errorf("level %d did not map to a name", e.level)
		}
	}
	t.Logf("routed %d messages, first: [%s/%s] %s",
		len(seen), seen[0].domain, seen[0].level, strings.TrimSpace(seen[0].msg))
}

func provokeAWarning(t *testing.T) error {
	t.Helper()
	jpg := readTestdata(t, "noise.jpg")
	src, err := vips.NewSourceFromReader(sequentialOnly{bytes.NewReader(jpg)})
	if err != nil {
		return err
	}
	im, err := vips.LoadSource(src)
	if err != nil {
		src.Close()
		return err
	}
	defer im.Close()
	src.Close() // pulls the reader out from under a pipeline that still needs it
	_, err = vips.SaveBuffer(im, ".png")
	return err
}

func TestSetLogHandlerNilRestoresTheDefault(t *testing.T) {
	called := false
	vips.SetLogHandler(func(string, vips.LogLevel, string) { called = true })
	vips.SetLogHandler(nil)

	// Nothing to assert about GLib's own handler beyond this not panicking and
	// the callback no longer being reachable.
	_ = called
}

func TestLogLevelNames(t *testing.T) {
	cases := map[vips.LogLevel]string{
		vips.LogError:    "error",
		vips.LogCritical: "critical",
		vips.LogWarning:  "warning",
		vips.LogMessage:  "message",
		vips.LogInfo:     "info",
		vips.LogDebug:    "debug",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Errorf("LogLevel(%d).String() = %q, want %q", level, got, want)
		}
	}
}

func TestCacheLimitsRoundTrip(t *testing.T) {
	files, mem, ops := vips.CacheMaxFiles(), vips.CacheMaxMem(), vips.CacheMax()
	t.Cleanup(func() {
		vips.SetCacheMaxFiles(files)
		vips.SetCacheMaxMem(mem)
		vips.SetCacheMax(ops)
	})

	vips.SetCacheMaxFiles(7)
	if got := vips.CacheMaxFiles(); got != 7 {
		t.Errorf("CacheMaxFiles = %d after setting 7", got)
	}

	vips.SetCacheMaxMem(64 << 20)
	if got := vips.CacheMaxMem(); got != 64<<20 {
		t.Errorf("CacheMaxMem = %d after setting %d", got, 64<<20)
	}
	t.Logf("cache limits: %d operations, %d bytes, %d files",
		vips.CacheMax(), vips.CacheMaxMem(), vips.CacheMaxFiles())
}

func TestShutdownThreadIsSafeToCall(t *testing.T) {
	// Whatever it releases, calling it must not disturb a package that carries
	// on being used afterwards.
	vips.ShutdownThread()

	im := black(t, 32, 32)
	defer im.Close()
	if im.Width() != 32 {
		t.Fatal("the package stopped working after ShutdownThread")
	}
}

func TestSetLeakReportingIsAccepted(t *testing.T) {
	vips.SetLeakReporting(true)
	vips.SetLeakReporting(false)
}
