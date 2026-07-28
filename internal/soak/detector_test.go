package soak

import (
	"os"
	"testing"
)

// TestLeakDetectorIsWorking leaks on purpose.
//
// The leak suite next door passes, and a passing leak check is worth exactly as
// much as the evidence that it would have failed. AddressSanitizer prints
// nothing when it finds nothing, so a green run and a run where leak detection
// was never switched on look identical from the outside.
//
// This deliberately loses a hundred C allocations. CI runs it under the same
// flags as the real suite and requires the run to fail; if it passes, the
// detector is off and every other leak result that day means nothing.
//
// It is skipped unless asked for, because it exists to fail.
func TestLeakDetectorIsWorking(t *testing.T) {
	if os.Getenv("VIPSX_PROVE_LEAK_CHECK") != "1" {
		t.Skip("set VIPSX_PROVE_LEAK_CHECK=1 to run; this test leaks on purpose")
	}

	// Enough blocks, and enough distance from the allocating frame, that none
	// can still be reachable from a stale register or stack slot.
	for range 2000 {
		leakOneBlock()
	}
	t.Log("leaked 2000 blocks of 1 KiB; the leak checker must now fail this run")
}
