package vips

import "fmt"

// Image returns a named output as an image. The caller owns it and should Close
// it when finished.
func (o Outputs) Image(name string) (*Image, error) {
	return get[*Image](o, name)
}

// Int returns a named output as an int. Enum and flags outputs come back as
// their integer value.
func (o Outputs) Int(name string) (int, error) { return get[int](o, name) }

// Int64 returns a named output as an int64, which is how 64-bit integer
// outputs arrive. No operation in libvips 8.18 produces one; the accessor
// exists so that one appearing in a newer libvips is readable rather than
// stuck behind a type assertion that can never pass.
func (o Outputs) Int64(name string) (int64, error) { return get[int64](o, name) }

// Float returns a named output as a float64.
func (o Outputs) Float(name string) (float64, error) { return get[float64](o, name) }

// Bool returns a named output as a bool.
func (o Outputs) Bool(name string) (bool, error) { return get[bool](o, name) }

// String returns a named output as a string.
func (o Outputs) String(name string) (string, error) { return get[string](o, name) }

// Bytes returns a named output as a byte slice, for buffer-producing saves.
func (o Outputs) Bytes(name string) ([]byte, error) { return get[[]byte](o, name) }

// Ints returns a named output as an int slice.
func (o Outputs) Ints(name string) ([]int, error) { return get[[]int](o, name) }

// Floats returns a named output as a float64 slice.
func (o Outputs) Floats(name string) ([]float64, error) { return get[[]float64](o, name) }

// Images returns a named output as an image slice. The caller owns each image.
func (o Outputs) Images(name string) ([]*Image, error) { return get[[]*Image](o, name) }

// Close releases every handle in the set — images, sources, targets and
// interpolators alike. Useful when an error path needs to discard a whole
// result.
func (o Outputs) Close() {
	for _, v := range o {
		closeOutput(v)
	}
}

func closeOutput(v any) {
	switch x := v.(type) {
	case *Image:
		x.Close()
	case []*Image:
		for _, im := range x {
			im.Close()
		}
	case *Source:
		x.Close()
	case *Target:
		x.Close()
	case *Interpolate:
		x.Close()
	}
}

func get[T any](o Outputs, name string) (T, error) {
	var zero T
	v, ok := o[name]
	if !ok {
		return zero, fmt.Errorf("vips: no output %q in result", name)
	}
	t, ok := v.(T)
	if !ok {
		return zero, fmt.Errorf("vips: output %q is %T, not %T", name, v, zero)
	}
	return t, nil
}
