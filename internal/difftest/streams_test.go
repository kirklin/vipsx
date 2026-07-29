package difftest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kirklin/vipsx/vips"
)

// The command line cannot be handed a source, a target or a byte buffer, so the
// operations taking one are invisible to the positional comparison next door.
// That left three of the eighteen marshalling paths never checked against
// anything independent, which is the largest hole this suite had.
//
// It closes without giving up independence. libvips names these operations in
// pairs: jpegload and jpegload_source read the same bytes by different routes
// and must produce the same pixels. So the CLI runs the file-based half and the
// binding runs the stream-based half, and the two are compared as before. The
// oracle is still an implementation this package did not write.

// pairFor returns the file-based sibling of a stream operation.
func pairFor(op string) (base, suffix string, ok bool) {
	for _, s := range []string{"_buffer", "_source", "_target"} {
		if !strings.HasSuffix(op, s) {
			continue
		}
		base = strings.TrimSuffix(op, s)
		if _, err := vips.Describe(base); err != nil {
			return "", "", false
		}
		return base, s, true
	}
	return "", "", false
}

func TestStreamsAgainstCLI(t *testing.T) {
	if _, err := exec.LookPath("vips"); err != nil {
		t.Skip("vips command line not on PATH")
	}

	dir := t.TempDir()
	fixture := buildFixtures(t, dir)[0]

	var loads, saves, skipped int
	kinds := map[vips.Kind]int{}

	for _, op := range vips.Operations() {
		base, suffix, ok := pairFor(op)
		if !ok {
			continue
		}

		t.Run(op, func(t *testing.T) {
			spec, err := vips.Describe(op)
			if err != nil {
				t.Skip("cannot describe")
			}

			switch {
			case suffix == "_source" || (suffix == "_buffer" && strings.Contains(op, "load")):
				loads++
				compareLoad(t, op, base, spec, fixture, dir, kinds, &skipped)
			case suffix == "_target" || suffix == "_buffer":
				saves++
				compareSave(t, op, base, spec, fixture, dir, kinds, &skipped)
			default:
				skipped++
				t.Skip("neither a load nor a save")
			}
		})
	}

	t.Logf("%d stream loads and %d stream saves paired with their file siblings, %d skipped",
		loads, saves, skipped)
	for _, k := range []vips.Kind{vips.KindSource, vips.KindTarget, vips.KindBlob} {
		t.Logf("  %s verified in %d comparisons", k, kinds[k])
	}
}

// compareLoad checks that reading through a source or a buffer gives the same
// pixels as the CLI reading the same file.
func compareLoad(t *testing.T, op, base string, spec *vips.OpSpec, fixture, dir string,
	kinds map[vips.Kind]int, skipped *int) {
	t.Helper()

	viaCLI := filepath.Join(dir, op+".cli.png")
	if out, err := runCLI(base, fixture, viaCLI); err != nil {
		*skipped++
		t.Skipf("the file-based sibling will not read this fixture: %s", firstLine(out))
	}

	var arg vips.Arg
	var kind vips.Kind
	switch {
	case hasArg(spec, "source", vips.KindSource):
		src, err := vips.NewSourceFromFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		arg, kind = vips.In("source", src), vips.KindSource

	case hasArg(spec, "buffer", vips.KindBlob):
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		arg, kind = vips.In("buffer", data), vips.KindBlob

	default:
		*skipped++
		t.Skip("takes neither a source nor a buffer")
	}

	outs, err := vips.Call(op, arg)
	if err != nil {
		*skipped++
		t.Skipf("the stream form will not read this fixture: %v", err)
	}
	defer outs.Close()

	im, err := outs.Image("out")
	if err != nil {
		t.Fatal(err)
	}
	viaGo := filepath.Join(dir, op+".go.png")
	if err := vips.Pngsave(im, viaGo, nil); err != nil {
		t.Fatal(err)
	}

	diff, err := maxAbsDiff(viaCLI, viaGo)
	if err != nil {
		t.Fatalf("comparing results: %v", err)
	}
	if diff != 0 {
		t.Errorf("%s disagrees with %s by %v; the %s marshalling is wrong",
			op, base, diff, kind)
		return
	}
	kinds[kind]++
}

// compareSave checks that writing through a target or to a buffer produces the
// same bytes as the CLI writing the same format to a file.
func compareSave(t *testing.T, op, base string, spec *vips.OpSpec, fixture, dir string,
	kinds map[vips.Kind]int, skipped *int) {
	t.Helper()

	src, err := vips.LoadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	ext := saverExtension(base)
	viaCLI := filepath.Join(dir, op+".cli"+ext)
	if out, err := runCLI(base, fixture, viaCLI); err != nil {
		*skipped++
		t.Skipf("the file-based sibling will not write this format: %s", firstLine(out))
	}

	var produced []byte
	var kind vips.Kind
	switch {
	case hasArg(spec, "target", vips.KindTarget):
		target, err := vips.NewTargetToMemory()
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()

		outs, err := vips.Call(op, vips.In("in", src), vips.In("target", target))
		if err != nil {
			*skipped++
			t.Skipf("the stream form will not write this fixture: %v", err)
		}
		outs.Close()
		if produced, err = target.Bytes(); err != nil {
			t.Fatalf("reading back the target: %v", err)
		}
		kind = vips.KindTarget

	default:
		outs, err := vips.Call(op, vips.In("in", src))
		if err != nil {
			*skipped++
			t.Skipf("the stream form will not write this fixture: %v", err)
		}
		defer outs.Close()
		if produced, err = outs.Bytes("buffer"); err != nil {
			*skipped++
			t.Skipf("produces no buffer: %v", err)
		}
		kind = vips.KindBlob
	}

	viaGo := filepath.Join(dir, op+".go"+ext)
	if err := os.WriteFile(viaGo, produced, 0o644); err != nil {
		t.Fatal(err)
	}

	diff, why, err := compareSaved(viaCLI, viaGo)
	if err != nil {
		t.Fatalf("comparing what was written: %v", err)
	}
	if why != "" {
		*skipped++
		t.Skip(why)
	}
	if diff != 0 {
		t.Errorf("%s disagrees with %s by %v; the %s marshalling is wrong",
			op, base, diff, kind)
		return
	}
	kinds[kind]++
}

func hasArg(spec *vips.OpSpec, name string, kind vips.Kind) bool {
	a, ok := spec.Arg(name)
	return ok && a.Kind == kind && a.Input
}

func firstLine(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return strings.TrimSpace(string(b))
}
