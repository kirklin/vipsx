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
	"strings"

	"github.com/kirklin/vipsx/vips"
)

// value is one argument, in the two forms the comparison needs.
//
// Both come from one place on purpose. If the CLI token and the Go value were
// chosen separately they could drift, and a drift would show up as one side
// failing while the other succeeded — reported as a defect in the binding when
// it was really a defect in the harness.
type value struct {
	cli string   // how the command line spells it
	arg vips.Arg // what the binding sends
}

// fixtures are the images a comparison runs on. Two, so operations taking an
// array of images have something to join.
type imageSet struct {
	paths  []string
	images []*vips.Image
}

// byName pins values for arguments whose meaning is known from their name.
//
// A wrong value is not dangerous here: the operation fails on both paths and
// the comparison is skipped, so the cost of a bad guess is lost coverage rather
// than a false accusation. These are the guesses that buy the most back.
var byName = map[string]any{
	// geometry, chosen to sit inside a 64x48 fixture
	"left": 4, "top": 4,
	"x": 4, "y": 4,
	"width": 24, "height": 16,
	"across": 2, "down": 2,
	"xfac": 2, "yfac": 2,
	"hspacing": 8, "vspacing": 8,
	"index": 2, "band": 0, "bandno": 0,
	"page": 0, "n": 1,

	// scalars
	"scale": 0.5, "sigma": 1.5, "radius": 2,
	"angle": 90.0, "exponent": 2.2, "factor": 2.0,
	"threshold": 0.5, "amount": 1.0,
	"cx": 8.0, "cy": 8.0, "a": 1.0, "b": 1.0,

	// text
	"text": "vipsx", "font": "sans 12",
	"filename": "", // filled in per call
	"basename": "tile",
}

// byKind is the fallback when the name says nothing.
var byKind = map[vips.Kind]any{
	vips.KindInt:         2,
	vips.KindUint64:      2,
	vips.KindDouble:      1.5,
	vips.KindBool:        true,
	vips.KindString:      "vipsx",
	vips.KindRefString:   "vipsx",
	vips.KindArrayInt:    []int{1, 2, 3},
	vips.KindArrayDouble: []float64{1, 2, 3},
}

// interpolator is shared by every comparison that needs one. Building it once
// keeps the comparisons from differing in which instance they used.
var interpolator = mustInterpolate("bicubic")

func mustInterpolate(name string) *vips.Interpolate {
	i, err := vips.NewInterpolate(name)
	if err != nil {
		panic("difftest: " + err.Error())
	}
	return i
}

// formatCLI renders a Go value the way the vips command line reads it.
func formatCLI(v any) (string, bool) {
	switch x := v.(type) {
	case int:
		return fmt.Sprint(x), true
	case float64:
		return fmt.Sprint(x), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case string:
		return x, true
	case []int:
		parts := make([]string, len(x))
		for i, n := range x {
			parts[i] = fmt.Sprint(n)
		}
		return strings.Join(parts, " "), true
	case []float64:
		parts := make([]string, len(x))
		for i, n := range x {
			parts[i] = fmt.Sprint(n)
		}
		return strings.Join(parts, " "), true
	}
	return "", false
}

// enumNick picks a member of an enum or flags type to test with.
//
// The operation's own default, when it has one. Choosing anything else is not
// safe: a value can be a perfectly good member of the enum type and still be
// one this particular operation does not handle, and libvips answers that by
// aborting the process rather than returning an error. Two were found this way.
// VipsBandFormat's members include VIPS_FORMAT_NOTSET at -1, and casting an
// image to it then saving trips an assertion in header.c. VipsOperationComplex2
// has a member complex2 does not implement, and reaching it aborts in
// complex.c. Neither is something a binding can catch.
//
// Sending the default explicitly still verifies what this harness is for: the
// value travels the whole marshalling path, and the comparison would notice if
// it arrived as something else. It costs the ability to detect an operation
// that ignores the argument entirely, which nothing here claimed to do.
func enumNick(a vips.ArgSpec) (nick string, number int, ok bool) {
	members := vips.EnumValues(a.TypeName)
	if len(members) == 0 {
		return "", 0, false
	}

	want, hasDefault := a.Default.(int)
	if hasDefault {
		for _, m := range members {
			if m.Value == want && m.Nick != "" {
				return m.Nick, m.Value, true
			}
		}
	}

	// No usable default: the lowest member at or above zero, which is the one
	// an operation is most likely to handle.
	best := vips.EnumValue{Value: -1}
	for _, m := range members {
		if m.Value < 0 || m.Nick == "" {
			continue
		}
		if best.Value < 0 || m.Value < best.Value {
			best = m
		}
	}
	if best.Nick == "" {
		return "", 0, false
	}
	return best.Nick, best.Value, true
}

// optionalValueFor produces an optional argument as the command line spells it:
// --name value, except booleans, which the CLI takes as a bare --name and
// rejects outright if given a value.
//
// Arrays are single-element here even though libvips would accept more. A
// colour or a per-band constant has to match the image's band count or the
// operation refuses it, and one element broadcasts to any width. Multi-element
// arrays are covered by the required-argument pass instead.
func optionalValueFor(a vips.ArgSpec, set *imageSet) (flags []string, arg vips.Arg, ok bool) {
	switch a.Kind {
	case vips.KindBool:
		return []string{"--" + a.Name}, vips.In(a.Name, true), true

	case vips.KindEnum, vips.KindFlags:
		nick, number, found := enumNick(a)
		if !found {
			return nil, vips.Arg{}, false
		}
		return []string{"--" + a.Name, nick}, vips.In(a.Name, number), true

	case vips.KindArrayInt:
		return []string{"--" + a.Name, "2"}, vips.In(a.Name, []int{2}), true

	case vips.KindArrayDouble:
		return []string{"--" + a.Name, "2"}, vips.In(a.Name, []float64{2}), true

	case vips.KindInterpolate:
		// The CLI takes an interpolator by name, and so does NewInterpolate.
		return []string{"--" + a.Name, "bicubic"}, vips.In(a.Name, interpolator), true

	case vips.KindImage, vips.KindArrayImage, vips.KindBlob, vips.KindSource,
		vips.KindTarget, vips.KindObject, vips.KindUnknown:
		return nil, vips.Arg{}, false
	}

	v, found := byName[a.Name]
	if !found || v == "" {
		v, found = byKind[a.Kind]
	}
	if !found {
		return nil, vips.Arg{}, false
	}
	cli, formatted := formatCLI(v)
	if !formatted {
		return nil, vips.Arg{}, false
	}
	return []string{"--" + a.Name, cli}, vips.In(a.Name, v), true
}

// valueFor produces both halves of one argument, or reports that this argument
// cannot be driven from the command line at all.
func valueFor(a vips.ArgSpec, set *imageSet, outPath string) (value, bool) {
	switch a.Kind {
	case vips.KindImage:
		if a.Output {
			// The CLI writes image outputs to a filename in argument position.
			return value{cli: outPath}, true
		}
		return value{cli: set.paths[0], arg: vips.In(a.Name, set.images[0])}, true

	case vips.KindArrayImage:
		return value{
			cli: strings.Join(set.paths, " "),
			arg: vips.In(a.Name, set.images),
		}, true

	case vips.KindEnum, vips.KindFlags:
		nick, number, ok := enumNick(a)
		if !ok {
			return value{}, false
		}
		return value{cli: nick, arg: vips.In(a.Name, number)}, true

	case vips.KindBlob, vips.KindSource, vips.KindTarget, vips.KindInterpolate,
		vips.KindObject, vips.KindUnknown:
		// Nothing the command line can be handed positionally.
		return value{}, false
	}

	v, ok := byName[a.Name]
	if !ok || v == "" {
		v, ok = byKind[a.Kind]
	}
	if !ok {
		return value{}, false
	}

	// A double argument given an int from the name table still has to reach
	// libvips as a number; In accepts either, and the CLI reads either.
	cli, ok := formatCLI(v)
	if !ok {
		return value{}, false
	}
	return value{cli: cli, arg: vips.In(a.Name, v)}, true
}
