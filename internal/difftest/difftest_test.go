package difftest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// runCLI invokes the vips command line for a comparison.
//
// Pinned to one worker thread, and the binding is pinned to match. Several
// libvips operations reduce over the whole image with one accumulator per
// thread and combine them in completion order, so with threads free the last
// bit of the answer depends on scheduling: phasecor, invfft and stats all drift
// by one. Two implementations cannot be compared for exactness while the
// implementation is allowed to disagree with itself, so the comparison removes
// the freedom rather than tolerating the result.
func runCLI(args ...string) ([]byte, error) {
	cmd := exec.Command("vips", args...)
	cmd.Env = append(os.Environ(), "VIPS_CONCURRENCY=1")
	return cmd.CombinedOutput()
}

// buildFixtures makes the images a comparison runs on: a seeded noise image
// fanned out to three bands, and a second one so array-of-image arguments have
// something to work with.
//
// The noise is seeded. An oracle whose input changes every run cannot tell a
// real regression from an unlucky draw, and a difference that appears once and
// not again is worse than no signal at all.
func buildFixtures(t *testing.T, dir string) []string {
	t.Helper()
	run := func(args ...string) {
		if out, err := runCLI(args...); err != nil {
			t.Fatalf("building fixtures: vips %v: %v\n%s", args, err, out)
		}
	}
	gray := filepath.Join(dir, "gray.png")
	rgb := filepath.Join(dir, "rgb.png")
	second := filepath.Join(dir, "rgb2.png")

	run("gaussnoise", gray, "64", "48", "--seed", "42")
	run("bandjoin_const", gray, rgb, "100 180")
	run("gaussnoise", filepath.Join(dir, "gray2.png"), "64", "48", "--seed", "99")
	run("bandjoin_const", filepath.Join(dir, "gray2.png"), second, "60 220")

	return []string{rgb, second}
}

// fixtures returns the image sets to drive the comparison with. The seeded
// synthetic pair is always included so the suite is self-contained; setting
// VIPSX_IMAGE_DIR adds real photographs, which exercise larger sizes, real
// colour profiles and EXIF that synthetic noise never produces.
func fixtures(t *testing.T, dir string) map[string][]string {
	t.Helper()
	synthetic := buildFixtures(t, dir)
	out := map[string][]string{"synthetic": synthetic}

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
			// Scaled down first. This compares two implementations against each
			// other; running it on full-resolution photographs measures
			// throughput instead, and turns a half-minute suite into a
			// quarter-hour one without checking anything extra.
			small := filepath.Join(dir, "fixture-"+sanitise(e.Name())+".png")
			cliOut, err := runCLI("thumbnail", filepath.Join(extra, e.Name()), small, "96")
			if err != nil {
				t.Logf("skipping %s: %s", e.Name(), firstLine(cliOut))
				continue
			}
			// Paired with a synthetic second image so array arguments still work.
			out[e.Name()] = []string{small, synthetic[1]}
		}
	}
	return out
}

// plan is one operation's comparison, with both sides derived from the same
// argument values.
type plan struct {
	op       string
	cliArgs  []string    // positional arguments for `vips <op> ...`
	callArgs []vips.Arg  // the same values, for the binding
	outName  string      // the required image output to compare
	outPath  string      // where the CLI was told to write it
	kinds    []vips.Kind // the marshalling paths this comparison exercises
	// savesToFile marks operations that write a file instead of returning an
	// image; for those the thing compared is what landed on disk.
	savesToFile bool
	skipWhy     string // non-empty when this operation cannot be driven
}

// saverExtension guesses the file suffix a save operation expects, from its own
// name. A wrong guess makes the save fail on both paths, which is a skip rather
// than an accusation.
func saverExtension(op string) string {
	base := strings.TrimSuffix(op, "save")
	switch base {
	case "jpeg":
		return ".jpg"
	case "tiff":
		return ".tif"
	case "heif":
		return ".heic"
	case "jp2k":
		return ".jp2"
	case "nifti":
		return ".nii"
	case "rad":
		return ".hdr"
	case "matrix":
		return ".mat"
	case "vips":
		return ".v"
	}
	return "." + base
}

// planForSaver handles operations that write a file rather than returning an
// image. The comparison is the same shape, but the thing compared is what
// landed on disk.
//
// This matters out of proportion to the operation count: the save operations
// are where the keep flags live, and where most of the format options live, so
// leaving them out means never checking those marshalling paths at all.
func planForSaver(op string, spec *vips.OpSpec, set *imageSet, dir string) (plan, bool) {
	filename, ok := spec.Arg("filename")
	if !ok || filename.Kind != vips.KindString || !filename.Required {
		return plan{}, false
	}
	for _, a := range spec.Args {
		if !a.Deprecated && a.Required && a.Output {
			return plan{}, false // it returns something, so it is not a plain saver
		}
	}

	out := filepath.Join(dir, op+".cli"+saverExtension(op))
	p := plan{op: op, outPath: out, savesToFile: true}

	for _, a := range spec.Args {
		if a.Deprecated || !a.Required || !a.Input {
			continue
		}
		if a.Name == "filename" {
			p.cliArgs = append(p.cliArgs, out)
			p.callArgs = append(p.callArgs, vips.In("filename", out))
			p.kinds = append(p.kinds, a.Kind)
			continue
		}
		v, ok := valueFor(a, set, "")
		if !ok {
			return plan{}, false
		}
		p.cliArgs = append(p.cliArgs, v.cli)
		p.callArgs = append(p.callArgs, v.arg)
		p.kinds = append(p.kinds, a.Kind)
	}
	return p, true
}

// planFor works out how to invoke an operation both ways.
//
// The command line takes an operation's required arguments positionally, in the
// order libvips declares them, with image outputs given as filenames. Building
// both sides from one walk of that declaration keeps them in step.
func planFor(op string, set *imageSet, dir string) plan {
	p := plan{op: op}

	spec, err := vips.Describe(op)
	if err != nil {
		p.skipWhy = "cannot describe"
		return p
	}

	if saver, ok := planForSaver(op, spec, set, dir); ok {
		return saver
	}

	imageOutputs := 0
	for _, a := range spec.Args {
		if a.Deprecated || !a.Required {
			continue
		}

		outPath := ""
		if a.Output && a.Kind == vips.KindImage {
			imageOutputs++
			outPath = filepath.Join(dir, fmt.Sprintf("%s.cli.%d.png", op, imageOutputs))
			if p.outName == "" {
				p.outName, p.outPath = a.Name, outPath
			}
		} else if a.Output {
			// A required non-image output, such as the number of regions
			// labelregions reports. The CLI prints it rather than writing a
			// file, so it takes no argument position.
			continue
		}

		v, ok := valueFor(a, set, outPath)
		if !ok {
			p.skipWhy = fmt.Sprintf("argument %q is a %s, which the CLI cannot be handed",
				a.Name, a.Kind)
			return p
		}
		p.cliArgs = append(p.cliArgs, v.cli)
		if a.Input {
			p.callArgs = append(p.callArgs, v.arg)
			p.kinds = append(p.kinds, a.Kind)
		}
	}

	if imageOutputs == 0 {
		p.skipWhy = "produces no image to compare"
	}
	return p
}

// withOptionals adds every optional argument the command line can express.
//
// Most booleans and flags in libvips are optional, so a comparison that sends
// only required arguments never exercises those marshalling paths at all. This
// pass does, at the cost of a lower acceptance rate: an operation that dislikes
// one of the values rejects the whole call, on both sides equally, and the
// comparison is skipped rather than counted against the binding.
func withOptionals(p plan, set *imageSet, dir string) plan {
	if p.skipWhy != "" {
		return p
	}
	spec, err := vips.Describe(p.op)
	if err != nil {
		p.skipWhy = "cannot describe"
		return p
	}

	q := p
	q.cliArgs = append([]string{}, p.cliArgs...)
	q.callArgs = append([]vips.Arg{}, p.callArgs...)
	q.kinds = append([]vips.Kind{}, p.kinds...)
	if p.savesToFile {
		q.outPath = filepath.Join(dir, p.op+".cli.opt"+saverExtension(p.op))
	} else {
		q.outPath = filepath.Join(dir, p.op+".cli.opt.png")
	}
	q.cliArgs = replaceOut(q.cliArgs, p.outPath, q.outPath)
	for i, a := range q.callArgs {
		if a.Name() == "filename" {
			q.callArgs[i] = vips.In("filename", q.outPath)
		}
	}

	added := 0
	for _, a := range spec.Args {
		if a.Deprecated || a.Required || !a.Input {
			continue
		}
		flags, arg, ok := optionalValueFor(a, set)
		if !ok {
			continue
		}
		q.cliArgs = append(q.cliArgs, flags...)
		q.callArgs = append(q.callArgs, arg)
		q.kinds = append(q.kinds, a.Kind)
		added++
	}
	if added == 0 {
		q.skipWhy = "no optional arguments the CLI can express"
	}
	return q
}

// maxAbsDiff reports the largest absolute per-pixel difference between two
// images. Zero means the two paths produced identical pixels.
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

	sub, err := vips.Subtract(ia, ib)
	if err != nil {
		return 0, err
	}
	defer sub.Close()

	abs, err := vips.Abs(sub)
	if err != nil {
		return 0, err
	}
	defer abs.Close()

	return vips.Max(abs, nil)
}

// compareSaved compares what two save calls wrote.
//
// Bytes first, which is the strictest thing available and works for formats
// nothing can read back: raw pixel dumps, and whatever magicksave decided to
// produce. Falling back to pixels covers formats that embed something variable
// in a header. Some savers write a directory rather than a file, and those are
// reported as unverifiable rather than quietly passed.
func compareSaved(cliPath, goPath string) (diff float64, why string, err error) {
	for _, p := range []string{cliPath, goPath} {
		info, statErr := os.Stat(p)
		if statErr != nil {
			return 0, "the saver wrote nothing at the path it was given", nil
		}
		if info.IsDir() {
			return 0, "this saver writes a directory, which this harness cannot compare", nil
		}
	}

	a, err := os.ReadFile(cliPath)
	if err != nil {
		return 0, "", err
	}
	b, err := os.ReadFile(goPath)
	if err != nil {
		return 0, "", err
	}
	if bytes.Equal(a, b) {
		return 0, "", nil
	}

	// Not byte-identical: fall back to the pixels, if the format can be read.
	diff, err = maxAbsDiff(cliPath, goPath)
	if err != nil {
		return 0, fmt.Sprintf("bytes differ and the format cannot be read back (%v)", err), nil
	}
	return diff, "", nil
}

// fourierNoise is the allowance for operations that go through FFTW.
//
// FFTW picks its algorithm at run time, and the choice is not stable across
// processes: the same transform planned two ways gives answers that differ in
// the last bits, which becomes one unit after rounding to eight bits per
// sample. Both sides here are the same libvips calling the same FFTW, so
// neither is wrong; they simply cannot be required to agree exactly.
//
// This is a measured list, not a guess, and not a blanket epsilon. Pinning
// VIPS_CONCURRENCY to one on both sides — which does fix the thread-order drift
// in stats — leaves phasecor still disagreeing on roughly one run in ten, and
// re-sampling the CLI ten times per mismatch still misses it. Anything not
// named here is required to match to the bit.
func fourierNoise(op string) float64 {
	switch {
	case strings.Contains(op, "fft"), op == "phasecor", op == "spectrum":
		return 1
	}
	return 0
}

// bindingIsStable reports whether the binding agrees with itself on an
// operation, by running it again in this process.
//
// The other half of oracleIsStable. A mismatch means one of two things: the
// binding is wrong, or one of the two sides is not reproducible. Asking only
// the command line answers half the question and leaves a rare disagreement
// looking like a defect with no evidence either way.
func bindingIsStable(t *testing.T, p plan, dir string, first string) (bool, float64) {
	t.Helper()
	const reruns = 5
	worst := 0.0
	for i := range reruns {
		again := filepath.Join(dir, fmt.Sprintf("%s.gorecheck%d.png", p.op, i))

		args := p.callArgs
		if p.savesToFile {
			again = filepath.Join(dir, fmt.Sprintf("%s.gorecheck%d%s", p.op, i, saverExtension(p.op)))
			args = make([]vips.Arg, len(p.callArgs))
			copy(args, p.callArgs)
			for j, a := range args {
				if a.Name() == "filename" {
					args[j] = vips.In("filename", again)
				}
			}
		}

		outs, err := vips.Call(p.op, args...)
		if err != nil {
			t.Fatalf("re-running the binding: %v", err)
		}
		if !p.savesToFile {
			im, err := outs.Image(p.outName)
			if err != nil {
				outs.Close()
				t.Fatalf("re-running the binding: %v", err)
			}
			if err := vips.Pngsave(im, again, nil); err != nil {
				outs.Close()
				t.Fatalf("re-running the binding: %v", err)
			}
		}
		outs.Close()

		var d float64
		if p.savesToFile {
			d, _, err = compareSaved(first, again)
		} else {
			d, err = maxAbsDiff(first, again)
		}
		if err != nil {
			t.Fatalf("comparing two binding runs: %v", err)
		}
		worst = max(worst, d)
	}
	return worst == 0, worst
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
func oracleIsStable(t *testing.T, p plan, dir string) (bool, float64) {
	t.Helper()
	// This catches operations whose answer moves between runs for reasons other
	// than FFTW planning — stats and the other whole-image reductions. It only
	// runs after a mismatch, so the cost is paid rarely.
	const reruns = 5
	worst := 0.0
	for i := range reruns {
		again := filepath.Join(dir, fmt.Sprintf("%s.recheck%d.png", p.op, i))
		args := append([]string{p.op}, replaceOut(p.cliArgs, p.outPath, again)...)
		if out, err := runCLI(args...); err != nil {
			t.Fatalf("re-running the CLI: %v\n%s", err, out)
		}
		var d float64
		var err error
		if p.savesToFile {
			d, _, err = compareSaved(p.outPath, again)
		} else {
			d, err = maxAbsDiff(p.outPath, again)
		}
		if err != nil {
			t.Fatalf("comparing two CLI runs: %v", err)
		}
		worst = max(worst, d)
	}
	return worst == 0, worst
}

func replaceOut(args []string, from, to string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == from {
			out[i] = to
		} else {
			out[i] = a
		}
	}
	return out
}

func TestAgainstCLI(t *testing.T) {
	if _, err := exec.LookPath("vips"); err != nil {
		t.Skip("vips command line not on PATH")
	}

	dir := t.TempDir()
	ops := vips.Operations()

	for name, paths := range fixtures(t, dir) {
		t.Run(name, func(t *testing.T) {
			compareAll(t, filepath.Join(dir, sanitise(name)), paths, ops)
		})
	}
}

func sanitise(name string) string {
	return strings.NewReplacer("/", "_", " ", "_", ".", "_").Replace(name)
}

func compareAll(t *testing.T, dir string, paths []string, ops []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	set := &imageSet{paths: paths}
	for _, p := range paths {
		im, err := vips.LoadFile(p)
		if err != nil {
			t.Fatalf("loading fixture %s: %v", p, err)
		}
		defer im.Close()
		set.images = append(set.images, im)
	}

	var compared, undrivable, rejected, unstable, mismatched, unverifiable int
	var err error
	var undrivableWhy = map[string]int{}
	// Which marshalling paths the oracle actually exercised. Counting operations
	// flatters the result: what matters is whether every way of turning a Go
	// value into a libvips argument has been checked against something
	// independent, and an operation count says nothing about that.
	var kindsSeen = map[vips.Kind]int{}

	compare := func(t *testing.T, p plan, suffix string) {
		t.Helper()
		if p.skipWhy != "" {
			undrivable++
			undrivableWhy[p.skipWhy]++
			t.Skip(p.skipWhy)
		}
		op := p.op
		viaGo := filepath.Join(dir, op+suffix+".go.png")
		if p.savesToFile {
			viaGo = filepath.Join(dir, op+suffix+".go"+saverExtension(op))
		}

		cliOut, cliErr := runCLI(append([]string{op}, p.cliArgs...)...)

		callArgs := p.callArgs
		if p.savesToFile {
			callArgs = make([]vips.Arg, len(p.callArgs))
			copy(callArgs, p.callArgs)
			for i, a := range callArgs {
				if a.Name() == "filename" {
					callArgs[i] = vips.In("filename", viaGo)
				}
			}
		}

		outs, goErr := vips.Call(op, callArgs...)
		if goErr == nil {
			defer outs.Close()
			if !p.savesToFile {
				if im, err := outs.Image(p.outName); err == nil {
					goErr = vips.Pngsave(im, viaGo, nil)
				} else {
					goErr = err
				}
			}
		}

		// Operations that reject these values on both sides tell us nothing.
		if cliErr != nil && goErr != nil {
			rejected++
			t.Skipf("rejected by both paths")
		}
		if cliErr != nil {
			t.Fatalf("CLI failed but the binding succeeded\nvips %s\n%v\n%s",
				strings.Join(append([]string{op}, p.cliArgs...), " "), cliErr, cliOut)
		}
		if goErr != nil {
			t.Fatalf("binding failed but the CLI succeeded: %v", goErr)
		}

		var diff float64
		if p.savesToFile {
			var why string
			diff, why, err = compareSaved(p.outPath, viaGo)
			if err != nil {
				t.Fatalf("comparing what was written: %v", err)
			}
			if why != "" {
				unverifiable++
				t.Skip(why)
			}
		} else {
			diff, err = maxAbsDiff(p.outPath, viaGo)
			if err != nil {
				t.Fatalf("comparing results: %v", err)
			}
		}
		compared++
		for _, k := range p.kinds {
			kindsSeen[k]++
		}
		if diff == 0 {
			return
		}
		if allowed := fourierNoise(op); diff <= allowed {
			unstable++
			t.Skipf("within FFTW's planning noise: differs by %v, allowed %v",
				diff, allowed)
		}
		// A mismatch is only evidence against the binding once both sides have
		// been shown to repeat themselves. Ask each of them.
		cliStable, cliDrift := oracleIsStable(t, p, dir)
		goStable, goDrift := bindingIsStable(t, p, dir, viaGo)
		if !cliStable || !goStable {
			unstable++
			t.Skipf("not reproducible: the CLI drifts by %v across runs, the binding "+
				"by %v, and they differ from each other by %v", cliDrift, goDrift, diff)
		}
		mismatched++
		t.Errorf("pixels differ by %v, and both sides repeat themselves exactly, "+
			"so this is the binding disagreeing with libvips rather than noise", diff)
	}

	for _, op := range ops {
		required := planFor(op, set, dir)
		t.Run(op, func(t *testing.T) { compare(t, required, "") })
		// A second pass carrying every optional argument the CLI can express,
		// which is the only way the boolean and flags marshalling ever gets
		// looked at: almost none of those are required arguments.
		t.Run(op+"+options", func(t *testing.T) {
			compare(t, withOptionals(required, set, dir), ".opt")
		})
	}

	t.Logf("%d compared against the CLI, %d rejected these values, "+
		"%d not drivable from the CLI, %d wrote something unreadable, "+
		"%d skipped as not reproducible, %d mismatched",
		compared, rejected, undrivable, unverifiable, unstable, mismatched)

	// Sources, targets and buffers cannot be handed to the command line at all,
	// so they are verified by TestStreamsAgainstCLI instead, which pairs each
	// stream operation with its file-based sibling. Counting them as unverified
	// here would misreport what the suite covers.
	elsewhere := map[vips.Kind]bool{
		vips.KindSource: true, vips.KindTarget: true, vips.KindBlob: true,
	}
	var missing []string
	for k := vips.KindBool; k <= vips.KindObject; k++ {
		if kindsSeen[k] == 0 && !elsewhere[k] {
			missing = append(missing, k.String())
		}
	}
	t.Logf("argument kinds verified here: %d of 17 (source, target and blob are "+
		"covered by TestStreamsAgainstCLI)", 17-len(missing)-len(elsewhere))
	if len(missing) > 0 {
		t.Logf("  still never verified against anything independent: %s",
			strings.Join(missing, " "))
	}

	reasons := make([]string, 0, len(undrivableWhy))
	for why := range undrivableWhy {
		reasons = append(reasons, why)
	}
	sort.Slice(reasons, func(i, j int) bool {
		return undrivableWhy[reasons[i]] > undrivableWhy[reasons[j]]
	})
	for _, why := range reasons[:min(len(reasons), 6)] {
		t.Logf("  not drivable, %3d ops: %s", undrivableWhy[why], why)
	}
}

func TestMain(m *testing.M) {
	// Match the command line, which every comparison pins to one thread.
	vips.SetConcurrency(1)
	os.Exit(m.Run())
}
