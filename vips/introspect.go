package vips

/*
#cgo pkg-config: vips
#include "vipsx.h"
*/
import "C"

import (
	"fmt"
	"sort"
	"sync"
	"unsafe"
)

// Kind is the marshalling class of an argument. These eighteen values cover
// every argument of every libvips operation; KindUnknown means libvips grew a
// type this package has not been taught, and is always a hard error rather than
// a guess.
type Kind int

const (
	KindUnknown Kind = iota
	KindBool
	KindInt
	KindUint64
	KindDouble
	KindString
	KindRefString
	KindEnum
	KindFlags
	KindImage
	KindArrayInt
	KindArrayDouble
	KindArrayImage
	KindBlob
	KindSource
	KindTarget
	KindInterpolate
	KindObject
)

var kindNames = [...]string{
	"unknown", "bool", "int", "uint64", "double", "string", "refstring",
	"enum", "flags", "image", "[]int", "[]double", "[]image", "blob",
	"source", "target", "interpolate", "object",
}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// ArgSpec describes one argument of an operation, as libvips reports it.
type ArgSpec struct {
	Name       string
	Blurb      string
	Kind       Kind
	TypeName   string // GType name, e.g. "VipsInteresting"
	Required   bool
	Input      bool
	Output     bool
	Deprecated bool

	// Default is the value libvips uses when this argument is omitted, or nil
	// when the type has no meaningful default. It is here for documentation and
	// code generation only. The call path never reads it: a supplied argument
	// is always sent, so the default is never silently substituted for a value
	// the caller actually wrote.
	Default any
}

// OpSpec describes one operation.
type OpSpec struct {
	Name        string
	Description string
	Args        []ArgSpec

	byName map[string]ArgSpec
}

// Arg looks up one argument by name.
func (s *OpSpec) Arg(name string) (ArgSpec, bool) {
	a, ok := s.byName[name]
	return a, ok
}

// RequiredOutputs lists the outputs libvips always produces. These are fetched
// automatically when a caller does not name any explicitly.
func (s *OpSpec) RequiredOutputs() []string {
	var names []string
	for _, a := range s.Args {
		if a.Output && a.Required && !a.Deprecated {
			names = append(names, a.Name)
		}
	}
	return names
}

func decodeDefault(kind Kind, ca C.VipsxArgSpec) any {
	switch kind {
	case KindBool:
		return ca.i_default != 0
	case KindInt, KindEnum, KindFlags:
		return int(ca.i_default)
	case KindUint64:
		return int64(ca.i_default)
	case KindDouble:
		return float64(ca.d_default)
	case KindString, KindRefString:
		if ca.s_default == nil {
			return ""
		}
		return C.GoString(ca.s_default)
	default:
		return nil
	}
}

var (
	specMu    sync.RWMutex
	specCache = map[string]*OpSpec{}
)

// Describe returns the introspected signature of an operation. Results are
// cached, so the reflection cost is paid once per operation rather than once
// per call.
func Describe(operation string) (*OpSpec, error) {
	specMu.RLock()
	spec, ok := specCache[operation]
	specMu.RUnlock()
	if ok {
		return spec, nil
	}

	cname := C.CString(operation)
	defer C.free(unsafe.Pointer(cname))

	cspec := C.vipsx_op_spec(cname)
	if cspec == nil {
		return nil, &Error{Op: operation, Message: "no such operation"}
	}
	defer C.vipsx_op_spec_free(cspec)

	spec = &OpSpec{
		Name:        C.GoString(cspec.name),
		Description: C.GoString(cspec.description),
		byName:      map[string]ArgSpec{},
	}

	n := int(cspec.n_args)
	if n > 0 {
		cargs := unsafe.Slice(cspec.args, n)
		for _, ca := range cargs {
			flags := int(ca.flags)
			a := ArgSpec{
				Name:       C.GoString(ca.name),
				Blurb:      C.GoString(ca.blurb),
				Kind:       Kind(ca.kind),
				TypeName:   C.GoString(ca.type_name),
				Required:   flags&C.VIPSX_FLAG_REQUIRED != 0,
				Input:      flags&C.VIPSX_FLAG_INPUT != 0,
				Output:     flags&C.VIPSX_FLAG_OUTPUT != 0,
				Deprecated: flags&C.VIPSX_FLAG_DEPRECATED != 0,
			}
			if ca.has_default != 0 {
				a.Default = decodeDefault(a.Kind, ca)
			}
			spec.Args = append(spec.Args, a)
			spec.byName[a.Name] = a
		}
	}

	specMu.Lock()
	specCache[operation] = spec
	specMu.Unlock()
	return spec, nil
}

// Operations lists every non-deprecated operation the installed libvips
// provides, sorted. Nothing here is hardcoded: a newer libvips reports more.
func Operations() []string {
	var count C.int
	cnames := C.vipsx_list_operations(&count)
	if cnames == nil || count == 0 {
		return nil
	}
	defer C.vipsx_strv_free(cnames, count)

	// A subclass that does not set its own nickname inherits the parent's, so
	// the walk can report the same operation twice. VipsCrop does exactly that
	// and shows up as a second extract_area.
	seen := make(map[string]bool, int(count))
	names := make([]string, 0, int(count))
	for _, c := range unsafe.Slice(cnames, int(count)) {
		name := C.GoString(c)
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EnumValue is one member of a libvips enum or flags type.
type EnumValue struct {
	Name  string
	Nick  string
	Value int
}

var ensureTypes sync.Once

// EnumValues reports the members of a GType by name, e.g. "VipsInteresting".
//
// GObject registers types the first time something uses them, so a lookup can
// miss simply because no operation has been built yet. On a miss every
// operation class is initialised once and the lookup is retried, which makes
// the result depend on the installed libvips rather than on what the program
// happened to call first.
func EnumValues(typeName string) []EnumValue {
	cname := C.CString(typeName)
	defer C.free(unsafe.Pointer(cname))

	var count C.int
	cvals := C.vipsx_enum_values(cname, &count)
	if cvals == nil || count == 0 {
		ensureTypes.Do(func() { C.vipsx_ensure_types() })
		cvals = C.vipsx_enum_values(cname, &count)
	}
	if cvals == nil || count == 0 {
		return nil
	}
	defer C.vipsx_enum_values_free(cvals, count)

	out := make([]EnumValue, 0, int(count))
	for _, cv := range unsafe.Slice(cvals, int(count)) {
		out = append(out, EnumValue{
			Name:  C.GoString(cv.name),
			Nick:  C.GoString(cv.nick),
			Value: int(cv.value),
		})
	}
	return out
}
