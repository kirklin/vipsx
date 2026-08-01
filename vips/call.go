package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unsafe"
)

// Arg is one argument of a call: either an input value or a request for an
// optional output.
type Arg struct {
	name  string
	value any
	isOut bool
}

// In supplies an input argument.
//
// Every In reaches libvips. A zero, false or empty string is a real value here,
// not a request for the default: In("band", 0) sets band to 0. To accept the
// libvips default for an argument, leave it out of the call entirely.
func In(name string, value any) Arg { return Arg{name: name, value: value} }

// Out requests an optional output by name. Required outputs are always
// returned and need not be named.
func Out(name string) Arg { return Arg{name: name, isOut: true} }

// Outputs holds the values an operation produced, keyed by libvips argument
// name. Images in here own a reference and should be Closed when done.
type Outputs map[string]any

// Call invokes any libvips operation by name.
//
// The operation does not need to be known to this package: the signature is
// read from the installed libvips at runtime, so an operation added in a newer
// libvips works without changes here.
//
// An argument the operation modifies in place — the image of a draw operation
// — is never the caller's. libvips writes into that argument rather than
// producing an output, and the object a caller holds may be shared: identical
// calls served from the operation cache return the same image, so drawing on
// it directly would draw on every holder's copy at once. Call substitutes a
// private memory copy, lets libvips modify that, and returns it in Outputs
// under the argument's name:
//
//	outs, err := vips.Call("draw_rect",
//	    vips.In("image", im), vips.In("ink", []float64{255}),
//	    vips.In("left", 10), vips.In("top", 10),
//	    vips.In("width", 100), vips.In("height", 100))
//	drawn, err := outs.Image("image")   // the result; im itself is untouched
func Call(operation string, args ...Arg) (Outputs, error) {
	if err := Startup(); err != nil {
		return nil, err
	}

	spec, err := Describe(operation)
	if err != nil {
		return nil, err
	}

	var (
		ar       arena
		cargs    []C.VipsxArg
		outNames []string
		// acquired holds the extra reference marshalling takes on every handle
		// argument. The references, not runtime.KeepAlive, are what make a
		// concurrent Close on an argument safe: Close revokes the handle but
		// cannot free an object this call still holds.
		acquired []unsafe.Pointer
		// mutated holds the private copies made for modify arguments. They are
		// handed to the caller through Outputs on success; on any failure they
		// are closed here rather than left to the collector.
		mutated   map[string]*Image
		handedOff bool
	)
	defer ar.free()
	defer func() {
		for _, p := range acquired {
			C.vipsx_object_unref(p)
		}
		if !handedOff {
			for _, im := range mutated {
				im.Close()
			}
		}
	}()

	for _, a := range args {
		as, ok := spec.Arg(a.name)
		if !ok {
			return nil, &Error{
				Op:      operation,
				Message: fmt.Sprintf("no argument %q; has %s", a.name, spec.argList()),
			}
		}

		if a.isOut {
			if !as.Output {
				return nil, &Error{
					Op:      operation,
					Message: fmt.Sprintf("argument %q is not an output", a.name),
				}
			}
			outNames = append(outNames, a.name)
			continue
		}

		if !as.Input {
			return nil, &Error{
				Op:      operation,
				Message: fmt.Sprintf("argument %q is an output, not an input", a.name),
			}
		}

		value := a.value
		if as.Modify {
			// The operation writes into this argument. The caller's object may
			// be shared through the operation cache, so what libvips gets is a
			// private copy, returned to the caller through Outputs.
			if as.Kind != KindImage {
				return nil, &Error{
					Op: operation,
					Message: fmt.Sprintf(
						"argument %q is modified in place and its kind %s is not supported",
						a.name, as.Kind),
				}
			}
			private, err := mutableCopy(a.value, as)
			if err != nil {
				return nil, &Error{Op: operation, Message: err.Error()}
			}
			if mutated == nil {
				mutated = map[string]*Image{}
			}
			mutated[a.name] = private
			value = private
		}

		var ca C.VipsxArg
		ca.name = ar.cstring(a.name)
		ca.kind = C.int(as.Kind)
		if err := marshal(&ar, as, value, &ca, &acquired); err != nil {
			return nil, &Error{Op: operation, Message: err.Error()}
		}
		cargs = append(cargs, ca)
	}

	// With no explicit request, hand back whatever libvips always produces.
	if len(outNames) == 0 {
		outNames = spec.RequiredOutputs()
	} else {
		outNames = append(outNames, spec.RequiredOutputs()...)
		outNames = dedupe(outNames)
	}

	couts := make([]C.VipsxOut, len(outNames))
	for i, name := range outNames {
		couts[i].name = ar.cstring(name)
	}

	cop := ar.cstring(operation)

	var argPtr *C.VipsxArg
	if len(cargs) > 0 {
		argPtr = &cargs[0]
	}
	var outPtr *C.VipsxOut
	if len(couts) > 0 {
		outPtr = &couts[0]
	}

	// With isolation on, the call and the drain of the error buffer happen
	// together, so a message cannot be another failure's. See SetErrorIsolation
	// for why that is a mode and not the default.
	if errorIsolation.Load() {
		errorIsolMu.Lock()
		defer errorIsolMu.Unlock()
	}

	rc := C.vipsx_call(cop, argPtr, C.int(len(cargs)), outPtr, C.int(len(couts)))

	if rc != 0 {
		return nil, &Error{Op: operation, Message: lastError()}
	}
	defer C.vipsx_out_clear(outPtr, C.int(len(couts)))

	res := make(Outputs, len(couts))
	for i, name := range outNames {
		v, err := unmarshal(&couts[i])
		if err != nil {
			// Release anything already handed to us before bailing out.
			for _, prior := range res {
				closeOutput(prior)
			}
			// And the references C took for outputs not yet unmarshalled:
			// nothing will ever wrap them, and vipsx_out_clear leaves .p alone.
			for j := i + 1; j < len(couts); j++ {
				if couts[j].p != nil {
					C.vipsx_object_unref(couts[j].p)
					couts[j].p = nil
				}
			}
			return nil, &Error{Op: operation, Message: err.Error()}
		}
		// An optional output the operation never produced comes back nil.
		// Leaving it out of the map lets the accessor say "no output" rather
		// than handing over a typed nil that panics on first use.
		if v == nil {
			continue
		}
		res[name] = v
	}
	// The private copies made for modify arguments are outputs in every sense
	// that matters: they carry what the operation produced. Hand them over
	// under their argument names.
	for name, im := range mutated {
		res[name] = im
	}
	handedOff = true
	return res, nil
}

// mutableCopy makes the private, memory-backed copy Call substitutes for an
// argument the operation would otherwise modify in place.
func mutableCopy(v any, spec ArgSpec) (*Image, error) {
	im, ok := v.(*Image)
	if !ok {
		return nil, typeErr(spec, v, "image handle")
	}
	if isNilPointer(v) {
		return nil, fmt.Errorf("argument %q: handle is nil", spec.Name)
	}
	p := im.tryAcquire()
	if p == nil {
		return nil, fmt.Errorf("argument %q: handle is closed", spec.Name)
	}
	defer im.release(p)

	cp := C.vipsx_image_mutable_copy(p)
	if cp == nil {
		return nil, fmt.Errorf("argument %q: copying for in-place modification: %s",
			spec.Name, lastError())
	}
	return wrapImage(unsafe.Pointer(cp)), nil
}

func (s *OpSpec) argList() string {
	var in, out []string
	for _, a := range s.Args {
		if a.Deprecated {
			continue
		}
		switch {
		case a.Input:
			in = append(in, a.Name)
		case a.Output:
			out = append(out, a.Name)
		}
	}
	sort.Strings(in)
	sort.Strings(out)
	return fmt.Sprintf("inputs [%s], outputs [%s]",
		strings.Join(in, " "), strings.Join(out, " "))
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Go value to C payload.
//
// Dispatch is on the Kind libvips reported for the argument, never on a guess
// from the argument's name. An argument whose type this package does not know
// is rejected here rather than being passed along as something plausible.
// ---------------------------------------------------------------------------

func marshal(ar *arena, spec ArgSpec, v any, dst *C.VipsxArg, acquired *[]unsafe.Pointer) error {
	switch spec.Kind {
	case KindBool:
		b, ok := asBool(v)
		if !ok {
			return typeErr(spec, v, "bool")
		}
		if b {
			dst.i = 1
		}

	case KindInt, KindUint64:
		n, ok := asInt(v)
		if !ok {
			return typeErr(spec, v, "integer")
		}
		dst.i = C.gint64(n)

	case KindFlags:
		n, ok := asInt(v)
		if !ok {
			return typeErr(spec, v, "integer")
		}
		// Bits outside the type are checked here because GLib does not reject
		// them: property validation masks unknown bits off silently, so a
		// typo'd flag would simply not happen — the wrong metadata kept, the
		// wrong page skipped — with nothing saying so.
		if !isFlagsValue(spec.TypeName, n) {
			return fmt.Errorf("argument %q: %#x has bits outside %s, whose members are %s",
				spec.Name, n, spec.TypeName, enumMembers(spec.TypeName))
		}
		dst.i = C.gint64(n)

	case KindEnum:
		n, ok := asInt(v)
		if !ok {
			return typeErr(spec, v, "integer")
		}
		// Checked against the type's members before it is sent. libvips does
		// not validate these: an out-of-range value is accepted, stored, and
		// then trips a g_assert deep inside a later operation, which aborts the
		// whole process rather than returning an error. Nothing a caller passes
		// should be able to do that.
		if !isEnumMember(spec.TypeName, int(n)) {
			return fmt.Errorf("argument %q: %d is not a member of %s, valid values are %s",
				spec.Name, n, spec.TypeName, enumMembers(spec.TypeName))
		}
		dst.i = C.gint64(n)

	case KindDouble:
		f, ok := asFloat(v)
		if !ok {
			return typeErr(spec, v, "number")
		}
		dst.d = C.double(f)

	case KindString, KindRefString:
		s, ok := asString(v)
		if !ok {
			return typeErr(spec, v, "string")
		}
		dst.s = ar.cstring(s)

	case KindImage, KindSource, KindTarget, KindInterpolate, KindObject:
		obj, ok := v.(cObject)
		if !ok {
			return typeErr(spec, v, spec.Kind.String()+" handle")
		}
		// A typed nil is not a nil interface: (*Image)(nil) satisfies cObject
		// and passes obj == nil, so it has to be looked through here. It used
		// to reach the method call below and panic on the nil receiver.
		if obj == nil || isNilPointer(v) {
			return fmt.Errorf("argument %q: handle is nil", spec.Name)
		}
		p := obj.acquireObject()
		if p == nil {
			return fmt.Errorf("argument %q: handle is closed", spec.Name)
		}
		*acquired = append(*acquired, p)
		dst.p = p

	case KindArrayInt:
		xs, ok := asIntSlice(v)
		if !ok {
			return typeErr(spec, v, "[]int")
		}
		dst.arr, dst.n = ar.ints(xs)

	case KindArrayDouble:
		xs, ok := asFloatSlice(v)
		if !ok {
			return typeErr(spec, v, "[]float64")
		}
		dst.arr, dst.n = ar.doubles(xs)

	case KindArrayImage:
		ims, ok := asImageSlice(v)
		if !ok {
			return typeErr(spec, v, "[]*Image")
		}
		ptrs := make([]unsafe.Pointer, len(ims))
		for i, im := range ims {
			p := im.acquireObject()
			if p == nil {
				return fmt.Errorf("argument %q: image %d is nil or closed", spec.Name, i)
			}
			// Appended one at a time so the ones already taken are released
			// even when a later element turns out closed.
			*acquired = append(*acquired, p)
			ptrs[i] = p
		}
		dst.arr, dst.n = ar.pointers(ptrs)

	case KindBlob:
		b, ok := asBytes(v)
		if !ok {
			return typeErr(spec, v, "[]byte")
		}
		dst.p, dst.n = ar.bytes(b)

	default:
		return fmt.Errorf(
			"argument %q has type %s, which this package does not know how to send",
			spec.Name, spec.TypeName)
	}
	return nil
}

func unmarshal(out *C.VipsxOut) (any, error) {
	switch Kind(out.kind) {
	case KindBool:
		return out.i != 0, nil
	case KindInt, KindEnum, KindFlags:
		return int(out.i), nil
	case KindUint64:
		return int64(out.i), nil
	case KindDouble:
		return float64(out.d), nil
	case KindString, KindRefString:
		return C.GoString(out.s), nil
	case KindImage:
		// nil for an optional output the operation never produced; Call skips
		// storing it, so the accessor reports "no output" instead of handing
		// over a handle that panics on first use.
		if out.p == nil {
			return nil, nil
		}
		return wrapImage(out.p), nil

	// Each of these used to be wrapped as an *Image, which is a lie about the
	// type rather than a leak: the reference counting works out, and then a
	// caller reads Width off what is really a VipsTarget. No operation in
	// libvips 8.18 has such an output, so nothing has ever hit it — which is
	// the argument for handling it now rather than after one appears.
	case KindSource:
		if out.p == nil {
			return nil, nil
		}
		s := &Source{}
		s.init(out.p)
		return s, nil
	case KindTarget:
		if out.p == nil {
			return nil, nil
		}
		t := &Target{}
		t.init(out.p)
		return t, nil
	case KindInterpolate:
		if out.p == nil {
			return nil, nil
		}
		i := &Interpolate{}
		i.init(out.p)
		return i, nil
	case KindObject:
		// A bare GObject has no handle type here, and guessing one would be the
		// mistake above. Release the reference C took for us and say so.
		if out.p != nil {
			C.vipsx_object_unref(out.p)
		}
		return nil, fmt.Errorf("output %q is a plain GObject, which this package has no type for",
			C.GoString(out.name))

	case KindBlob:
		return goBytes(out.arr, out.n), nil
	case KindArrayInt:
		n := int(out.n)
		src := unsafe.Slice((*C.int)(out.arr), n)
		xs := make([]int, n)
		for i, c := range src {
			xs[i] = int(c)
		}
		return xs, nil
	case KindArrayDouble:
		n := int(out.n)
		src := unsafe.Slice((*C.double)(out.arr), n)
		xs := make([]float64, n)
		for i, c := range src {
			xs[i] = float64(c)
		}
		return xs, nil
	case KindArrayImage:
		n := int(out.n)
		src := unsafe.Slice((*unsafe.Pointer)(out.arr), n)
		ims := make([]*Image, n)
		for i, p := range src {
			ims[i] = wrapImage(p)
		}
		return ims, nil
	default:
		return nil, fmt.Errorf("output %q has an unsupported type",
			C.GoString(out.name))
	}
}

var (
	enumMu    sync.RWMutex
	enumCache = map[string]map[int]string{}
)

// enumValuesFor caches the members of an enum type, since marshalling consults
// them on every call.
func enumValuesFor(typeName string) map[int]string {
	enumMu.RLock()
	members, ok := enumCache[typeName]
	enumMu.RUnlock()
	if ok {
		return members
	}

	members = map[int]string{}
	for _, v := range EnumValues(typeName) {
		members[v.Value] = v.Nick
	}
	enumMu.Lock()
	enumCache[typeName] = members
	enumMu.Unlock()
	return members
}

func isEnumMember(typeName string, value int) bool {
	members := enumValuesFor(typeName)
	if len(members) == 0 {
		return true // nothing known about this type; let libvips decide
	}
	_, ok := members[value]
	return ok
}

// isFlagsValue reports whether every set bit belongs to the flags type. Unlike
// an enum, a flags value is a combination, so membership is a mask test rather
// than a lookup.
func isFlagsValue(typeName string, value int64) bool {
	members := enumValuesFor(typeName)
	if len(members) == 0 {
		return true // nothing known about this type; let libvips decide
	}
	var mask int64
	for v := range members {
		mask |= int64(v)
	}
	return value&^mask == 0
}

func enumMembers(typeName string) string {
	members := enumValuesFor(typeName)
	pairs := make([]string, 0, len(members))
	for value, nick := range members {
		pairs = append(pairs, fmt.Sprintf("%d (%s)", value, nick))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ", ")
}

func typeErr(spec ArgSpec, got any, want string) error {
	return fmt.Errorf("argument %q (%s) wants %s, got %T",
		spec.Name, spec.TypeName, want, got)
}

// isNilPointer looks through an interface at a typed nil pointer.
func isNilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// ---------------------------------------------------------------------------
// Permissive but explicit Go value coercion.
// ---------------------------------------------------------------------------

func asBool(v any) (bool, bool) {
	if b, ok := v.(bool); ok {
		return b, true
	}
	if n, ok := asInt(v); ok {
		return n != 0, true
	}
	return false, false
}

func asInt(v any) (int64, bool) {
	if b, ok := v.(bool); ok {
		if b {
			return 1, true
		}
		return 0, true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f == float64(int64(f)) {
			return int64(f), true
		}
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	}
	return 0, false
}

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.String {
		return rv.String(), true
	}
	return "", false
}

func asBytes(v any) ([]byte, bool) {
	switch x := v.(type) {
	case []byte:
		return x, true
	case string:
		return []byte(x), true
	}
	return nil, false
}

// asIntSlice accepts a slice, or a lone value as a one-element array. libvips
// array arguments routinely take a single number to mean "same for all bands".
func asIntSlice(v any) ([]int, bool) {
	if n, ok := asInt(v); ok {
		return []int{int(n)}, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	xs := make([]int, rv.Len())
	for i := range xs {
		n, ok := asInt(rv.Index(i).Interface())
		if !ok {
			return nil, false
		}
		xs[i] = int(n)
	}
	return xs, true
}

func asFloatSlice(v any) ([]float64, bool) {
	if f, ok := asFloat(v); ok {
		return []float64{f}, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	xs := make([]float64, rv.Len())
	for i := range xs {
		f, ok := asFloat(rv.Index(i).Interface())
		if !ok {
			return nil, false
		}
		xs[i] = f
	}
	return xs, true
}

func asImageSlice(v any) ([]*Image, bool) {
	switch x := v.(type) {
	case []*Image:
		return x, true
	case *Image:
		return []*Image{x}, true
	}
	return nil, false
}

// Name reports which libvips argument this refers to.
func (a Arg) Name() string { return a.name }
