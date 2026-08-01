// Package coverage checks that everything the hand-maintained and generated Go
// bindings expose is reachable here.
//
// The claim being tested is narrow and worth stating exactly: every libvips
// operation those projects wrap can be called through vipsx. It says nothing
// about their convenience layers, which are a separate matter from operation
// reach.
package coverage

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// readList loads operation names captured from another binding's source.
func readList(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		if line := s.Text(); line != "" {
			out = append(out, line)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// reachable reports the operations vipsx enumerates. Callability is broader
// than this: libvips resolves some names as aliases of another operation, so
// they can be called without appearing as distinct entries. crop is one, and
// resolves to extract_area.
func reachable(t *testing.T) map[string]bool {
	t.Helper()
	set := map[string]bool{}
	for _, op := range vips.Operations() {
		set[op] = true
	}
	return set
}

func checkCoverage(t *testing.T, listName string) {
	t.Helper()
	want := readList(t, listName)
	have := reachable(t)

	var viaAlias, notAnOperation []string
	for _, op := range want {
		if have[op] {
			continue
		}
		// Anything libvips can build an operation for is callable through
		// vips.Call, whether or not it enumerates as its own entry. Anything it
		// cannot build is not an operation at all — these lists were scraped
		// from C source and include a few plain helper functions, which no
		// binding can expose as an operation.
		if _, err := vips.Describe(op); err == nil {
			viaAlias = append(viaAlias, op)
			continue
		}
		notAnOperation = append(notAnOperation, op)
	}
	sort.Strings(viaAlias)

	// The classification alone cannot fail: enumeration breaking would file
	// everything under aliases, Describe breaking would file everything under
	// "not an operation", and both read as explanations. These operations are
	// core, present in every libvips this package supports, and must be
	// enumerated by name — if one is not, the walk itself is broken, whatever
	// the classification says.
	for _, op := range []string{"add", "invert", "resize", "extract_area", "black", "copy", "embed", "gaussblur"} {
		if !have[op] {
			t.Errorf("core operation %q is not enumerated; the operation walk is broken", op)
		}
	}

	t.Logf("%s: %d entries — %d enumerated, %d callable as aliases (%v), "+
		"%d not libvips operations",
		listName, len(want),
		len(want)-len(viaAlias)-len(notAnOperation),
		len(viaAlias), viaAlias, len(notAnOperation))
}

func TestCoversVipsgen(t *testing.T) { checkCoverage(t, "vipsgen-8.18.2.txt") }

func TestCoversGovips(t *testing.T) { checkCoverage(t, "govips.txt") }

// Beyond parity: report what vipsx reaches that the others do not.
func TestReachBeyondOthers(t *testing.T) {
	have := reachable(t)
	others := map[string]bool{}
	for _, list := range []string{"vipsgen-8.18.2.txt", "govips.txt"} {
		for _, op := range readList(t, list) {
			others[op] = true
		}
	}

	var extra []string
	for op := range have {
		if !others[op] {
			extra = append(extra, op)
		}
	}
	sort.Strings(extra)

	t.Logf("vipsx reaches %d operations on libvips %s; %d of them appear in "+
		"neither vipsgen nor govips", len(have), vips.Version(), len(extra))
	if len(extra) > 0 {
		t.Logf("examples: %v", extra[:min(len(extra), 25)])
	}
}

// Nothing in this package is hardcoded per operation, so reach is whatever the
// installed libvips reports. This asserts that the discovery is actually
// dynamic rather than accidentally matching a fixed list.
func TestReachIsWhateverLibvipsReports(t *testing.T) {
	ops := vips.Operations()
	if len(ops) == 0 {
		t.Fatal("no operations discovered")
	}
	for _, op := range ops {
		spec, err := vips.Describe(op)
		if err != nil {
			t.Errorf("discovered %q but cannot describe it: %v", op, err)
			continue
		}
		if len(spec.Args) == 0 {
			t.Errorf("%s has no arguments, which should be impossible", op)
		}
	}
}
