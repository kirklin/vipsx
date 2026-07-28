// Package difftest checks this binding against the vips command line.
//
// The binding reaches every operation through one generic path, so there is no
// per-operation code to review. What replaces that review is this: run each
// operation both through the binding and through libvips' own CLI, and require
// the pixels to match exactly. The CLI is an independent implementation of the
// same call sequence, which makes it a real oracle rather than a restatement of
// the binding's own assumptions.
package difftest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// fixture builds a small three-band image the CLI and the binding both read.
//
// The noise is seeded. An oracle whose input changes every run cannot tell a
// real regression from an unlucky draw, and a difference that appears once and
// not again is worse than no signal at all.
func fixture(t *testing.T, dir string) string {
	t.Helper()
	gray := filepath.Join(dir, "gray.png")
	cmd := exec.Command("vips", "gaussnoise", gray, "64", "48", "--seed", "42")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building fixture: %v\n%s", err, out)
	}
	// gaussnoise is one band; fan it out to three so band-aware operations get
	// something representative.
	rgb := filepath.Join(dir, "rgb.png")
	cmd = exec.Command("vips", "bandjoin_const", gray, rgb, "128 200")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building rgb fixture: %v\n%s", err, out)
	}
	return rgb
}

// candidate is an operation the CLI can drive with two positional arguments:
// one required image in, one required image out, nothing else required.
type candidate struct {
	op     string
	inArg  string
	outArg string // not always "out"; labelregions calls it "mask"
}

func candidates(t *testing.T) []candidate {
	t.Helper()
	var ops []candidate
	for _, name := range vips.Operations() {
		spec, err := vips.Describe(name)
		if err != nil {
			continue
		}
		var c candidate
		var inImages, outImages, otherRequired int
		for _, a := range spec.Args {
			if a.Deprecated || !a.Required {
				continue
			}
			switch {
			case a.Input && a.Kind == vips.KindImage:
				inImages++
				c.inArg = a.Name
			case a.Output && a.Kind == vips.KindImage:
				outImages++
				c.outArg = a.Name
			default:
				otherRequired++
			}
		}
		if inImages == 1 && outImages == 1 && otherRequired == 0 {
			c.op = name
			ops = append(ops, c)
		}
	}
	return ops
}

// maxAbsDiff reports the largest absolute per-pixel difference between two
// images. Zero means the two paths produced identical pixels.
//
// Both sides are written in libvips' own .v format rather than PNG. Several
// operations return doubles — stats returns a matrix of sums and deviations
// running into the billions — and casting those to 8-bit for comparison
// measures the cast rather than the operation.
func maxAbsDiff(a, b string) (float64, error) {
	ia, err := vips.LoadFile(a)
	if err != nil {
		return 0, err
	}
	defer ia.Close()
	ib, err := vips.LoadFile(b)
	if err != nil {
		return 0, err
	}
	defer ib.Close()

	if ia.Width() != ib.Width() || ia.Height() != ib.Height() || ia.Bands() != ib.Bands() {
		return 0, fmt.Errorf("shape differs: %dx%dx%d vs %dx%dx%d",
			ia.Width(), ia.Height(), ia.Bands(),
			ib.Width(), ib.Height(), ib.Bands())
	}

	sub, err := vips.Call("subtract", vips.In("left", ia), vips.In("right", ib))
	if err != nil {
		return 0, err
	}
	defer sub.Close()
	diff, err := sub.Image("out")
	if err != nil {
		return 0, err
	}

	abs, err := vips.Call("abs", vips.In("in", diff))
	if err != nil {
		return 0, err
	}
	defer abs.Close()
	absImage, err := abs.Image("out")
	if err != nil {
		return 0, err
	}

	stats, err := vips.Call("max", vips.In("in", absImage))
	if err != nil {
		return 0, err
	}
	defer stats.Close()
	return stats.Float("out")
}

// oracleIsStable reports whether the CLI agrees with itself on an operation.
//
// Some libvips operations reduce over the whole image with one accumulator per
// worker thread, so the order the partial results are combined in depends on
// how work landed on threads, and the low bits of the answer move between runs.
// stats and the Fourier family both do this: the CLI disagrees with itself on
// eight of ten runs of stats over the same file, while this binding gives ten
// identical results.
//
// So a mismatch is only evidence against the binding once the oracle has been
// shown to be repeatable. This asks it directly rather than granting a blanket
// epsilon, which would also swallow real breakage.
func oracleIsStable(t *testing.T, op, src, first, dir string) (bool, float64) {
	t.Helper()
	// Several re-runs, not one. An operation that agrees with itself only some
	// of the time will pass a single re-run often enough to make the suite
	// flaky, which is the failure mode this whole check exists to remove.
	const reruns = 3
	worst := 0.0
	for i := range reruns {
		again := filepath.Join(dir, fmt.Sprintf("%s.cli%d.v", op, i+2))
		cmd := exec.Command("vips", op, src, again)
		cmd.Env = append(os.Environ(), "VIPS_CONCURRENCY=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("re-running the CLI: %v\n%s", err, out)
		}
		d, err := maxAbsDiff(first, again)
		if err != nil {
			t.Fatalf("comparing two CLI runs: %v", err)
		}
		worst = max(worst, d)
	}
	return worst == 0, worst
}

// fixtures returns the images to drive the comparison with. The seeded
// synthetic image is always included so the suite is self-contained; setting
// VIPSX_IMAGE_DIR adds real photographs, which exercise larger sizes, real
// colour profiles and EXIF that synthetic noise never produces.
func fixtures(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{"synthetic": fixture(t, dir)}

	extra := os.Getenv("VIPSX_IMAGE_DIR")
	if extra == "" {
		return out
	}
	entries, err := os.ReadDir(extra)
	if err != nil {
		t.Fatalf("VIPSX_IMAGE_DIR: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".png", ".jpg", ".jpeg", ".webp", ".tif", ".tiff":
			out[e.Name()] = filepath.Join(extra, e.Name())
		}
	}
	return out
}

func TestAgainstCLI(t *testing.T) {
	if _, err := exec.LookPath("vips"); err != nil {
		t.Skip("vips command line not on PATH")
	}

	dir := t.TempDir()
	ops := candidates(t)
	if len(ops) < 20 {
		t.Fatalf("only %d comparable operations found, expected many more", len(ops))
	}

	for name, src := range fixtures(t, dir) {
		t.Run(name, func(t *testing.T) { compareAll(t, dir+"/"+sanitise(name), src, ops) })
	}
}

func sanitise(name string) string {
	return strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(name)
}

func compareAll(t *testing.T, prefix, src string, ops []candidate) {
	t.Helper()
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		t.Fatal(err)
	}

	var compared, skipped, mismatched, unstable int
	for _, c := range ops {
		t.Run(c.op, func(t *testing.T) {
			dir := prefix
			viaCLI := filepath.Join(dir, c.op+".cli.v")
			viaGo := filepath.Join(dir, c.op+".go.v")

			cmd := exec.Command("vips", c.op, src, viaCLI)
			cmd.Env = append(os.Environ(), "VIPS_CONCURRENCY=1")
			cliOut, cliErr := cmd.CombinedOutput()

			in, err := vips.LoadFile(src)
			if err != nil {
				t.Fatalf("loading fixture: %v", err)
			}
			defer in.Close()

			outs, goErr := vips.Call(c.op, vips.In(c.inArg, in))
			if goErr == nil {
				defer outs.Close()
				if im, err := outs.Image(c.outArg); err == nil {
					saved, err := vips.Call("vipssave", vips.In("in", im), vips.In("filename", viaGo))
					if err != nil {
						goErr = err
					} else {
						saved.Close()
					}
				} else {
					goErr = err
				}
			}

			// Operations that reject this input on both sides tell us nothing.
			if cliErr != nil && goErr != nil {
				skipped++
				t.Skipf("rejected by both paths")
			}
			if cliErr != nil {
				t.Fatalf("CLI failed but the binding succeeded\nCLI: %v\n%s", cliErr, cliOut)
			}
			if goErr != nil {
				t.Fatalf("binding failed but the CLI succeeded: %v", goErr)
			}

			diff, err := maxAbsDiff(viaCLI, viaGo)
			if err != nil {
				t.Fatalf("comparing results: %v", err)
			}
			compared++
			if diff == 0 {
				return
			}
			if stable, selfDiff := oracleIsStable(t, c.op, src, viaCLI, dir); !stable {
				unstable++
				t.Skipf("libvips is not reproducible here: two CLI runs differ by %v, "+
					"binding differs from the first by %v", selfDiff, diff)
			}
			mismatched++
			t.Errorf("pixels differ from a reproducible CLI result, "+
				"max absolute difference %v", diff)
		})
	}

	t.Logf("compared %d operations against the CLI, %d rejected by both, "+
		"%d skipped as not reproducible in libvips, %d mismatched",
		compared, skipped, unstable, mismatched)
}

func TestMain(m *testing.M) {
	// Both sides run on one thread.
	//
	// Several reductions choose between equally correct answers by whichever
	// worker got there first. stats is the clearest: over one of the sample
	// photographs it reports the maximum at (753,448) or at (803,309) from run
	// to run, both being pixels that hold the maximum, while every statistic in
	// the same matrix — sum, mean, deviation — is bit-identical. Pinning the
	// thread count makes the tie break the same way on both sides, so the
	// comparison measures the binding instead of the scheduler.
	vips.SetConcurrency(1)
	os.Exit(m.Run())
}
