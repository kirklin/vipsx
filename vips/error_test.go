package vips_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// libvips keeps one error buffer for the whole process, so concurrent failures
// interleave in it. With isolation on, one operation runs at a time and a
// message can only belong to the failure that produced it.
//
// Measured on this machine without isolation, 8 goroutines failing 200 times
// each get a message that does not mention their own filename about 88% of the
// time. The point of the test is the guarantee, not the number: with isolation
// on it has to be exactly zero.
func TestErrorIsolationAttributesEveryMessage(t *testing.T) {
	const goroutines, rounds = 8, 60

	vips.SetErrorIsolation(true)
	t.Cleanup(func() { vips.SetErrorIsolation(false) })

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		strayed []string
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mine := fmt.Sprintf("vipsx-attribution-%d", i)
			for r := 0; r < rounds; r++ {
				_, err := vips.Call("jpegload",
					vips.In("filename", "/nonexistent/"+mine+".jpg"))
				if err == nil {
					t.Errorf("loading a nonexistent file succeeded")
					return
				}
				if msg := err.Error(); !strings.Contains(msg, mine) {
					mu.Lock()
					if len(strayed) < 3 {
						strayed = append(strayed, msg)
					}
					mu.Unlock()
				}
			}
		}(i)
	}
	wg.Wait()

	if len(strayed) > 0 {
		t.Errorf("with isolation on, %d messages did not name their own call; first: %s",
			len(strayed), strayed[0])
	}
}

func TestErrorIsolationIsOffByDefault(t *testing.T) {
	if vips.ErrorIsolation() {
		t.Fatal("error isolation is on by default; it serialises every call")
	}
	vips.SetErrorIsolation(true)
	if !vips.ErrorIsolation() {
		t.Fatal("SetErrorIsolation(true) did not take")
	}
	vips.SetErrorIsolation(false)
	if vips.ErrorIsolation() {
		t.Fatal("SetErrorIsolation(false) did not take")
	}
}

// Whatever else is true of the buffer, draining it must not lose the message
// of a call that failed on its own.
func TestFailedCallCarriesADetail(t *testing.T) {
	_, err := vips.Call("jpegload", vips.In("filename", "/nonexistent/vipsx-detail.jpg"))
	if err == nil {
		t.Fatal("loading a nonexistent file succeeded")
	}
	var e *vips.Error
	if !asVipsError(err, &e) {
		t.Fatalf("got %T, want *vips.Error", err)
	}
	if e.Op != "jpegload" {
		t.Errorf("Op is %q, want jpegload", e.Op)
	}
	if strings.TrimSpace(e.Message) == "" {
		t.Error("the error carries no detail from libvips")
	}
}

func asVipsError(err error, target **vips.Error) bool {
	e, ok := err.(*vips.Error)
	if ok {
		*target = e
	}
	return ok
}
