package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"reflect"
	"runtime"
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
		keep     []any
	)
	defer ar.free()

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

		var ca C.VipsxArg
		ca.name = ar.cstring(a.name)
		ca.kind = C.int(as.Kind)
		if err := marshal(&ar, as, a.value, &ca, &keep); err != nil {
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

	rc := C.vipsx_call(cop, argPtr, C.int(len(cargs)), outPtr, C.int(len(couts)))
	runtime.KeepAlive(keep)

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
				if im, ok := prior.(*Image); ok {
					im.Close()
				}
			}
			return nil, &Error{Op: operation, Message: err.Error()}
		}
		res[name] = v
	}
	return res, nil
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

func marshal(ar *arena, spec ArgSpec, v any, dst *C.VipsxArg, keep *[]any) error {
	switch spec.Kind {
	case KindBool:
		b, ok := asBool(v)
		if !ok {
			return typeErr(spec, v, "bool")
		}
		if b {
			dst.i = 1
		}

	case KindInt, KindUint64, KindFlags:
		n, ok := asInt(v)
		if !ok {
			return typeErr(spec, v, "integer")
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
		p := obj.cPointer()
		if p == nil {
			return fmt.Errorf("argument %q: handle is closed", spec.Name)
		}
		dst.p = p
		*keep = append(*keep, v)

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
			if im == nil || im.cPointer() == nil {
				return fmt.Errorf("argument %q: image %d is nil or closed", spec.Name, i)
			}
			ptrs[i] = im.cPointer()
		}
		dst.arr, dst.n = ar.pointers(ptrs)
		*keep = append(*keep, v)

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
	case KindImage, KindSource, KindTarget, KindInterpolate, KindObject:
		return wrapImage(out.p), nil
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
