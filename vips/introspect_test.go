package vips_test

import (
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// The C side used to describe at most 64 arguments per operation and walk at
// most 2000 operations, dropping anything past either without a word. Neither
// ceiling was reached on libvips 8.18 — the widest operation takes 31 and there
// are 330 of them — so this guards the invariant rather than reproducing a
// failure: everything libvips reports has to survive the trip.
//
// The failure it prevents is a confusing one. A truncated argument list makes
// Call answer "no argument q" for an argument that exists and was simply never
// looked at.
func TestDescribeReportsEveryArgument(t *testing.T) {
	widest := ""
	most := 0
	for _, op := range vips.Operations() {
		spec, err := vips.Describe(op)
		if err != nil {
			t.Fatalf("describing %s: %v", op, err)
		}
		if len(spec.Args) > most {
			widest, most = op, len(spec.Args)
		}
		// Every argument in the list has to be addressable by name, or the
		// list and the lookup disagree about what the operation takes.
		for _, a := range spec.Args {
			if _, ok := spec.Arg(a.Name); !ok {
				t.Fatalf("%s reports argument %q but cannot look it up", op, a.Name)
			}
		}
	}

	t.Logf("widest operation is %s with %d arguments", widest, most)

	// A count landing exactly on a power of two is what truncation looks like.
	for _, cap := range []int{16, 32, 64, 128} {
		if most == cap {
			t.Errorf("the widest operation reports exactly %d arguments, "+
				"which is what a cap looks like rather than a coincidence", cap)
		}
	}
	if most < 20 {
		t.Errorf("the widest operation reports only %d arguments; "+
			"libvips has savers with far more", most)
	}
}

func TestOperationListIsNotTruncated(t *testing.T) {
	ops := vips.Operations()
	if len(ops) < 200 {
		t.Fatalf("only %d operations discovered", len(ops))
	}
	// The old walk stopped at 2000. Anything at or near a round ceiling is
	// worth a second look rather than a shrug.
	for _, cap := range []int{256, 512, 1024, 2000, 2048} {
		if len(ops) == cap {
			t.Errorf("exactly %d operations discovered, which looks like a cap", cap)
		}
	}
	t.Logf("%d operations on libvips %s", len(ops), vips.Version())
}

// Describe caches, so the second answer has to be the first one rather than a
// half-built spec left behind by a failed allocation.
func TestDescribeIsStableAcrossCalls(t *testing.T) {
	first, err := vips.Describe("tiffsave")
	if err != nil {
		t.Fatal(err)
	}
	second, err := vips.Describe("tiffsave")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Args) != len(second.Args) {
		t.Fatalf("described tiffsave with %d arguments, then %d",
			len(first.Args), len(second.Args))
	}
	if first.Name != second.Name || first.Description != second.Description {
		t.Fatal("two Describes of the same operation disagree")
	}
}

func TestEnumValuesAreComplete(t *testing.T) {
	// Interesting is small and stable; BandFormat is the one the pixel calls
	// depend on being right.
	for _, typeName := range []string{"VipsInteresting", "VipsBandFormat", "VipsKernel"} {
		vals := vips.EnumValues(typeName)
		if len(vals) == 0 {
			t.Errorf("%s reported no members", typeName)
			continue
		}
		for _, v := range vals {
			if v.Name == "" || v.Nick == "" {
				t.Errorf("%s has a member with an empty name or nick: %+v", typeName, v)
			}
		}
	}
}

func TestEnumValuesOfSomethingThatIsNotAnEnum(t *testing.T) {
	if vals := vips.EnumValues("VipsImage"); len(vals) != 0 {
		t.Errorf("VipsImage is not an enum but reported %d members", len(vals))
	}
	if vals := vips.EnumValues("NoSuchTypeAtAll"); len(vals) != 0 {
		t.Errorf("an unknown type reported %d members", len(vals))
	}
}
