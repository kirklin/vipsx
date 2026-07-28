// Command vipsx-gen writes the typed layer over the generic call path.
//
// It asks the installed libvips what operations exist and what they take, then
// emits one Go function and one options struct per operation. Nothing about the
// operations is written down here; changing the libvips this runs against
// changes the output.
//
// Two properties are deliberate. First, the generated code is pure Go: every
// function is a few lines that build arguments and hand them to vips.Call, so
// there is no generated C and no per-operation cgo to compile. Second, optional
// arguments are pointers rather than zero values, which is what lets a caller
// say "set this to zero" as distinct from "leave it alone" — the whole reason
// the call path never consults defaults.
//
//	go run ./cmd/vipsx-gen -out vips
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/kirklin/vipsx/vips"
)

func main() {
	out := flag.String("out", "vips", "directory to write generated files into")
	flag.Parse()

	ops := vips.Operations()
	log.Printf("libvips %s reports %d operations", vips.Version(), len(ops))

	g := &generator{
		enums:   map[string]string{},
		opNames: map[string]bool{},
	}

	specs := make([]*vips.OpSpec, 0, len(ops))
	for _, name := range ops {
		spec, err := vips.Describe(name)
		if err != nil {
			log.Printf("skipping %s: %v", name, err)
			continue
		}
		specs = append(specs, spec)
		g.opNames[exported(name)] = true
	}
	// Operation names are claimed first: they are the API people call, so an
	// enum whose short name would collide keeps its longer one instead.
	for _, spec := range specs {
		g.collectEnums(spec)
	}

	enumFile := filepath.Join(*out, "zz_generated_enums.go")
	if err := write(enumFile, g.renderEnums()); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s with %d types", enumFile, len(g.enums))

	opsFile := filepath.Join(*out, "zz_generated_ops.go")
	body, count, skipped := g.renderOps(specs)
	if err := write(opsFile, body); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s with %d operations (%d skipped)", opsFile, count, skipped)
}

func write(path string, src []byte) error {
	formatted, err := format.Source(src)
	if err != nil {
		// Keep the unformatted output so the syntax error can be read.
		_ = os.WriteFile(path+".broken", src, 0o644)
		return fmt.Errorf("formatting %s: %w (raw output in %s.broken)", path, err, path)
	}
	return os.WriteFile(path, formatted, 0o644)
}

type generator struct {
	// GType name -> Go type name, e.g. "VipsInteresting" -> "Interesting"
	enums map[string]string
	// Exported names already taken by operation functions.
	opNames map[string]bool
}

func (g *generator) collectEnums(spec *vips.OpSpec) {
	for _, a := range spec.Args {
		if a.Deprecated {
			continue
		}
		if a.Kind == vips.KindEnum || a.Kind == vips.KindFlags {
			if _, done := g.enums[a.TypeName]; done {
				continue
			}
			name := goEnumName(a.TypeName)
			// VipsForeignSubsample would shorten to Subsample, which the
			// subsample operation already owns.
			if g.opNames[name] || g.taken(name) {
				name = strings.TrimPrefix(a.TypeName, "Vips")
			}
			g.enums[a.TypeName] = name
		}
	}
}

func (g *generator) taken(name string) bool {
	for _, existing := range g.enums {
		if existing == name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Enums
// ---------------------------------------------------------------------------

func (g *generator) renderEnums() []byte {
	var b bytes.Buffer
	header(&b)

	names := make([]string, 0, len(g.enums))
	for gtype := range g.enums {
		names = append(names, gtype)
	}
	sort.Strings(names)

	for _, gtype := range names {
		goName := g.enums[gtype]
		values := vips.EnumValues(gtype)
		if len(values) == 0 {
			continue
		}

		fmt.Fprintf(&b, "// %s mirrors the libvips %s type.\ntype %s int\n\nconst (\n",
			goName, gtype, goName)
		seen := map[string]bool{}
		for _, v := range values {
			constName := goName + exported(v.Nick)
			if seen[constName] {
				continue
			}
			seen[constName] = true
			fmt.Fprintf(&b, "\t// %s is %q.\n\t%s %s = %d\n",
				constName, v.Nick, constName, goName, v.Value)
		}
		b.WriteString(")\n\n")

		// A String method makes these printable in logs and test failures.
		fmt.Fprintf(&b, "// String returns the libvips nickname for this value.\nfunc (v %s) String() string {\n\tswitch v {\n", goName)
		seenVal := map[int]bool{}
		for _, v := range values {
			if seenVal[v.Value] {
				continue
			}
			seenVal[v.Value] = true
			fmt.Fprintf(&b, "\tcase %d:\n\t\treturn %q\n", v.Value, v.Nick)
		}
		fmt.Fprintf(&b, "\t}\n\treturn \"%s(\" + itoa(int(v)) + \")\"\n}\n\n", goName)
	}

	b.WriteString(`// itoa avoids pulling strconv into generated code for one call site.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
`)
	return b.Bytes()
}

// ---------------------------------------------------------------------------
// Operations
// ---------------------------------------------------------------------------

func (g *generator) renderOps(specs []*vips.OpSpec) ([]byte, int, int) {
	var b bytes.Buffer
	header(&b)
	b.WriteString(`// Ptr returns a pointer to v, for filling in the optional fields of the
// options structs below.
//
// The fields are pointers on purpose. A nil field means "not supplied, let
// libvips use its default"; a pointer to the zero value means "set this to
// zero", which is a different thing and one that bindings modelling options as
// plain zero values cannot express at all.
//
//	vips.Resize(im, 0.5, &vips.ResizeOptions{Kernel: vips.Ptr(vips.KernelNearest)})
func Ptr[T any](v T) *T { return &v }

`)

	var count, skipped int
	seen := map[string]bool{}

	for _, spec := range specs {
		fn := exported(spec.Name)
		if fn == "" || seen[fn] {
			skipped++
			continue
		}

		code, ok := g.renderOp(spec, fn)
		if !ok {
			skipped++
			continue
		}
		seen[fn] = true
		b.WriteString(code)
		count++
	}
	return b.Bytes(), count, skipped
}

type param struct {
	arg    vips.ArgSpec
	goName string
	goType string
}

func (g *generator) renderOp(spec *vips.OpSpec, fn string) (string, bool) {
	var required, optional, outputs []param

	for _, a := range spec.Args {
		if a.Deprecated {
			continue
		}
		t, ok := g.goType(a)
		if !ok {
			return "", false // an argument this generator cannot type
		}
		p := param{arg: a, goName: identifier(a.Name), goType: t}

		switch {
		case a.Input && a.Required:
			required = append(required, p)
		case a.Input:
			p.goName = exported(a.Name)
			optional = append(optional, p)
		case a.Output && a.Required:
			outputs = append(outputs, p)
		}
	}

	var b strings.Builder
	optType := fn + "Options"

	// Options struct
	if len(optional) > 0 {
		fmt.Fprintf(&b, "// %s holds the optional arguments of %s.\n//\n"+
			"// A nil field is not sent, so libvips applies its own default. A non-nil\n"+
			"// field is sent exactly as given, including a pointer to zero.\ntype %s struct {\n",
			optType, spec.Name, optType)
		for _, p := range optional {
			if blurb := p.arg.Blurb; blurb != "" {
				fmt.Fprintf(&b, "\t// %s %s.\n", p.goName, strings.TrimRight(blurb, "."))
			}
			if d := formatDefault(p.arg); d != "" {
				fmt.Fprintf(&b, "\t// libvips default: %s\n", d)
			}
			fmt.Fprintf(&b, "\t%s *%s\n", p.goName, p.goType)
		}
		b.WriteString("}\n\n")
	}

	// Signature
	fmt.Fprintf(&b, "// %s runs the libvips %q operation: %s.\n",
		fn, spec.Name, strings.TrimRight(spec.Description, "."))
	for _, p := range required {
		if p.arg.Blurb != "" {
			fmt.Fprintf(&b, "//\n// %s: %s.\n", p.goName, strings.TrimRight(p.arg.Blurb, "."))
		}
	}

	var sigParams []string
	for _, p := range required {
		sigParams = append(sigParams, p.goName+" "+p.goType)
	}
	if len(optional) > 0 {
		sigParams = append(sigParams, "options *"+optType)
	}

	var returns []string
	for _, p := range outputs {
		returns = append(returns, p.goType)
	}
	returns = append(returns, "error")

	retSig := returns[0]
	if len(returns) > 1 {
		retSig = "(" + strings.Join(returns, ", ") + ")"
	}
	fmt.Fprintf(&b, "func %s(%s) %s {\n", fn, strings.Join(sigParams, ", "), retSig)

	// Body
	fmt.Fprintf(&b, "\targs := make([]Arg, 0, %d)\n", len(required)+len(optional))
	for _, p := range required {
		fmt.Fprintf(&b, "\targs = append(args, In(%q, %s))\n", p.arg.Name, castToArg(p))
	}
	if len(optional) > 0 {
		b.WriteString("\tif options != nil {\n")
		for _, p := range optional {
			fmt.Fprintf(&b, "\t\tif options.%s != nil {\n\t\t\targs = append(args, In(%q, %s))\n\t\t}\n",
				p.goName, p.arg.Name, derefToArg(p))
		}
		b.WriteString("\t}\n")
	}

	zeros := make([]string, 0, len(outputs))
	for _, p := range outputs {
		zeros = append(zeros, zeroValue(p.goType))
	}
	failReturn := func(errExpr string) string {
		parts := append(append([]string{}, zeros...), errExpr)
		return "return " + strings.Join(parts, ", ")
	}

	fmt.Fprintf(&b, "\touts, err := Call(%q, args...)\n\tif err != nil {\n\t\t%s\n\t}\n",
		spec.Name, failReturn("err"))

	if len(outputs) == 0 {
		b.WriteString("\touts.Close()\n\treturn nil\n}\n\n")
		return b.String(), true
	}

	for i, p := range outputs {
		accessor, cast := outputAccessor(p)
		fmt.Fprintf(&b, "\tv%d, err := outs.%s(%q)\n\tif err != nil {\n\t\touts.Close()\n\t\t%s\n\t}\n",
			i, accessor, p.arg.Name, failReturn("err"))
		if cast != "" {
			fmt.Fprintf(&b, "\tr%d := %s(v%d)\n", i, cast, i)
		} else {
			fmt.Fprintf(&b, "\tr%d := v%d\n", i, i)
		}
	}

	results := make([]string, 0, len(outputs)+1)
	for i := range outputs {
		results = append(results, fmt.Sprintf("r%d", i))
	}
	results = append(results, "nil")
	fmt.Fprintf(&b, "\treturn %s\n}\n\n", strings.Join(results, ", "))

	return b.String(), true
}

// goType maps an argument to the Go type the generated API exposes.
func (g *generator) goType(a vips.ArgSpec) (string, bool) {
	switch a.Kind {
	case vips.KindBool:
		return "bool", true
	case vips.KindInt:
		return "int", true
	case vips.KindUint64:
		return "int64", true
	case vips.KindDouble:
		return "float64", true
	case vips.KindString, vips.KindRefString:
		return "string", true
	case vips.KindEnum, vips.KindFlags:
		if name, ok := g.enums[a.TypeName]; ok {
			return name, true
		}
		return "int", true
	case vips.KindImage:
		return "*Image", true
	case vips.KindArrayInt:
		return "[]int", true
	case vips.KindArrayDouble:
		return "[]float64", true
	case vips.KindArrayImage:
		return "[]*Image", true
	case vips.KindBlob:
		return "[]byte", true
	case vips.KindSource:
		return "*Source", true
	case vips.KindTarget:
		return "*Target", true
	case vips.KindInterpolate:
		return "*Interpolate", true
	default:
		return "", false
	}
}

// castToArg renders a required parameter as something In accepts.
func castToArg(p param) string {
	if p.arg.Kind == vips.KindEnum || p.arg.Kind == vips.KindFlags {
		return "int(" + p.goName + ")"
	}
	return p.goName
}

// derefToArg renders an optional field, which is always a pointer.
func derefToArg(p param) string {
	if p.arg.Kind == vips.KindEnum || p.arg.Kind == vips.KindFlags {
		return "int(*options." + p.goName + ")"
	}
	return "*options." + p.goName
}

// outputAccessor names the Outputs method for a result, plus any cast needed to
// reach the declared return type.
func outputAccessor(p param) (accessor, cast string) {
	switch p.arg.Kind {
	case vips.KindBool:
		return "Bool", ""
	case vips.KindInt:
		return "Int", ""
	case vips.KindUint64:
		return "Int", "int64"
	case vips.KindDouble:
		return "Float", ""
	case vips.KindString, vips.KindRefString:
		return "String", ""
	case vips.KindEnum, vips.KindFlags:
		return "Int", p.goType
	case vips.KindImage:
		return "Image", ""
	case vips.KindArrayInt:
		return "Ints", ""
	case vips.KindArrayDouble:
		return "Floats", ""
	case vips.KindArrayImage:
		return "Images", ""
	case vips.KindBlob:
		return "Bytes", ""
	}
	return "Image", ""
}

func zeroValue(goType string) string {
	switch goType {
	case "bool":
		return "false"
	case "int", "int64":
		return "0"
	case "float64":
		return "0"
	case "string":
		return `""`
	}
	if strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "[]") {
		return "nil"
	}
	return goType + "(0)" // a generated enum
}

func formatDefault(a vips.ArgSpec) string {
	switch d := a.Default.(type) {
	case nil:
		return ""
	case string:
		if d == "" {
			return ""
		}
		return fmt.Sprintf("%q", d)
	case bool:
		return fmt.Sprint(d)
	default:
		return fmt.Sprint(d)
	}
}

// ---------------------------------------------------------------------------
// Identifiers
// ---------------------------------------------------------------------------

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	// not keywords, but shadowing these inside a generated body would be a bug
	"len": true, "cap": true, "new": true, "make": true, "copy": true,
	"args": true, "options": true, "outs": true, "err": true,
}

// words splits a libvips name into pieces. Names arrive as snake_case,
// kebab-case, or camelCase such as "LabQ2sRGB".
func words(name string) []string {
	var out []string
	var cur strings.Builder
	runes := []rune(name)

	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case unicode.IsUpper(r) && i > 0 &&
			(unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])):
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// exported turns a libvips name into an exported Go identifier, keeping
// acronym-shaped pieces such as sRGB and CMYK intact.
func exported(name string) string {
	var b strings.Builder
	for _, w := range words(name) {
		if w == "" {
			continue
		}
		r := []rune(w)
		// Leave a piece alone when it already carries capitals past the first
		// character, so LabQ2sRGB does not become Labq2Srgb.
		hasInnerUpper := false
		for _, c := range r[1:] {
			if unicode.IsUpper(c) {
				hasInnerUpper = true
				break
			}
		}
		if hasInnerUpper {
			b.WriteString(strings.ToUpper(string(r[0])) + string(r[1:]))
			continue
		}
		b.WriteString(strings.ToUpper(string(r[0])) + strings.ToLower(string(r[1:])))
	}
	s := b.String()
	if s != "" && unicode.IsDigit(rune(s[0])) {
		s = "Op" + s
	}
	return s
}

// identifier turns a libvips name into an unexported Go identifier for use as a
// function parameter.
func identifier(name string) string {
	s := exported(name)
	if s == "" {
		return "arg"
	}
	r := []rune(s)
	// Lowercase the leading run of capitals so RGB becomes rgb, not rGB.
	i := 0
	for i < len(r) && unicode.IsUpper(r[i]) {
		i++
	}
	if i > 1 && i < len(r) {
		i-- // keep the capital that starts the next word
	}
	lowered := strings.ToLower(string(r[:i])) + string(r[i:])
	if goKeywords[lowered] {
		return lowered + "_"
	}
	return lowered
}

func goEnumName(gtype string) string {
	name := strings.TrimPrefix(gtype, "Vips")
	name = strings.TrimPrefix(name, "Foreign")
	return name
}

func header(b *bytes.Buffer) {
	fmt.Fprintf(b, `// Code generated by cmd/vipsx-gen from libvips %s. DO NOT EDIT.
//
// Regenerate with:
//
//	go run ./cmd/vipsx-gen -out vips
//
// Every function here is a thin wrapper over Call. Nothing in this file knows
// anything the installed libvips did not report, and none of it is C.

package vips

`, vips.Version())
}
