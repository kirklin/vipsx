# vipsx

Go bindings for [libvips](https://www.libvips.org/), built on runtime introspection.

libvips describes its own operations through the GObject type system. vipsx reads
that description at runtime instead of wrapping operations one at a time, so a
single generic call path reaches every operation the installed libvips provides,
including ones added after this package was written.

```go
im, err := vips.LoadFile("photo.jpg")
if err != nil {
    return err
}
defer im.Close()

outs, err := vips.Call("thumbnail_image", vips.In("in", im), vips.In("width", 640))
if err != nil {
    return err
}
defer outs.Close()

thumb, _ := outs.Image("out")
webp, err := vips.SaveBuffer(thumb, ".webp", vips.In("Q", 82))
```

## Install

Needs libvips and a C toolchain.

```bash
brew install vips        # or: apt install libvips-dev
```

```bash
go get github.com/kirklin/vipsx
```

On macOS, cgo needs one flag to accept libvips' preprocessor options:

```bash
export CGO_CFLAGS_ALLOW=-Xpreprocessor
```

## Using it

Everything goes through `Call`, which takes an operation name and its arguments.
Argument names are libvips' own, the same ones `vips <operation>` prints.

```go
outs, err := vips.Call("gaussblur", vips.In("in", im), vips.In("sigma", 3.0))
```

Required outputs come back automatically. Optional ones are asked for by name:

```go
outs, err := vips.Call("min", vips.In("in", im), vips.Out("x"), vips.Out("y"))
x, _ := outs.Int("x")
y, _ := outs.Int("y")
```

Outputs are read with typed accessors: `Image`, `Int`, `Float`, `Bool`, `String`,
`Bytes`, `Ints`, `Floats`, `Images`. Images own a libvips reference; `Close` them,
or `outs.Close()` to release a whole result.

Loading and saving pick the format from content or extension:

```go
im, err := vips.LoadFile("in.heic")          // sniffs content
im, err := vips.LoadBuffer(data)
err = vips.SaveFile(im, "out.avif", vips.In("Q", 60))
buf, err := vips.SaveBuffer(im, ".jpg", vips.In("Q", 90))
```

To find out what an operation takes without leaving Go:

```go
spec, _ := vips.Describe("thumbnail")
for _, a := range spec.Args {
    fmt.Println(a.Name, a.Kind, a.Required, a.Input, a.Blurb, a.Default)
}
```

`vips.Operations()` lists everything the installed libvips can do, and
`vips.EnumValues("VipsInteresting")` gives an enum's members.

### A supplied argument is always sent

This is the one behavioural promise worth reading twice. Bindings that model
optional arguments as a struct of zero values cannot tell "I did not set this"
from "I set this to zero", and quietly drop the second. vipsx represents absence
by leaving the argument out of the call, so zero, false and "" are real values:

```go
vips.Call("hist_find", vips.In("in", im))                      // libvips default: all bands
vips.Call("hist_find", vips.In("in", im), vips.In("band", 0))  // band 0, and it means it
```

There are 357 optional arguments in libvips 8.18 whose default is not zero. Each
one is a place where the distinction matters.

## How it compares

Coverage is checked by a test, not asserted here. `internal/coverage` takes the
operation lists scraped from govips and vipsgen and requires every entry to be
reachable:

| | operations reached | binding source |
|---|---|---|
| vipsx | 330 | ~1,100 lines Go + ~700 lines C |
| vipsgen | 289 | ~6,400 line generator, ~31,000 lines generated per libvips version |
| govips | 185 | ~10,900 lines Go + ~7,900 lines C |

Both lists come back fully covered: every one of vipsgen's 293 scraped entries is
reachable (289 operations plus 4 that are C helpers, not operations), and so are
all 223 of govips' (185 operations, 2 reachable as aliases, 36 C helpers). 41
operations vipsx reaches appear in neither, mostly colourspace conversions.

`examples/gallery` runs 35 of these against a real photograph and writes an
index page, labelling each with the govips method it stands in for:

```bash
go run ./examples/gallery photo.jpg ./out && open ./out/index.html
```

vipsx does not ship a typed facade. govips and vipsgen give you
`img.Thumbnail(width, &ThumbnailOptions{...})` with compile-time argument
checking; here an unknown argument name or a wrong value type is a clear runtime
error rather than a compile error. That is a real trade and the reason those
projects exist. It is also the layer a generator can add on top of this one
without touching the call path.

## Correctness

There is no per-operation code to review, so review is replaced by an oracle.
`internal/difftest` runs every operation that takes one image and returns one
image through both this binding and the `vips` command line, and requires the
pixels to match exactly.

```bash
go test ./internal/difftest/
VIPSX_IMAGE_DIR=/path/to/photos go test ./internal/difftest/   # add real images
```

Both sides run pinned to one worker thread, and both write libvips' own `.v`
format rather than PNG. Neither is incidental. Several operations reduce over the
whole image and break ties by whichever thread arrived first: over one of the
sample photographs `stats` reports the maximum at (753,448) or at (803,309)
between runs, both being pixels that hold the maximum, while every statistic in
the same matrix — sum, mean, deviation — is bit-identical. And `stats` returns
doubles running into the billions, so comparing after a cast to 8-bit PNG
measures the cast instead of the operation. If an operation still disagrees, the
harness re-runs the CLI to ask whether libvips is reproducible there at all, and
reports the two cases separately.

`TestNoUnknownArgumentKinds` walks every argument of every operation and fails if
any falls outside the eighteen types the marshaller knows. A libvips upgrade that
introduces a new argument type breaks that test rather than silently marshalling
the new type as something plausible.

## Status

The generic call path is complete and tested. A generated typed layer on top of
it is the obvious next step and is not written yet.

## License

MIT
