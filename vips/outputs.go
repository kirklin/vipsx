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

// Close releases every image in the set. Useful when an error path needs to
// discard a whole result.
func (o Outputs) Close() {
	for _, v := range o {
		switch x := v.(type) {
		case *Image:
			x.Close()
		case []*Image:
			for _, im := range x {
				im.Close()
			}
		}
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
