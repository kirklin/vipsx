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

thumb, err := vips.ThumbnailImage(im, 640, &vips.ThumbnailImageOptions{
    Crop: vips.Ptr(vips.InterestingAttention),
})
if err != nil {
    return err
}
defer thumb.Close()

webp, err := vips.SaveBuffer(thumb, ".webp", vips.In("Q", 82))
```

That typed layer is generated from the installed libvips, and every function in
it is a few lines over one generic `Call`. Both are public: use the typed one by
default, drop to `Call` for an operation the generator has not been run against.

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

### The typed layer

`cmd/vipsx-gen` writes one function and one options struct per operation, plus
Go types for every libvips enum. On libvips 8.18 that is 330 functions and 47
enums, all pure Go: no generated C, so there is no per-operation cgo to compile.

```go
small, err := vips.Resize(im, 0.5, nil)                  // defaults throughout
blur, err := vips.Gaussblur(small, 2.0, nil)
gray, err := vips.Colourspace(blur, vips.InterpretationBW, nil)
avg, err := vips.Avg(gray)                               // scalar result
err = vips.Pngsave(gray, "out.png", nil)                 // no image result
```

Optional arguments are pointers, and that is the point:

```go
vips.Resize(im, 0.25, nil)                                       // libvips picks lanczos3
vips.Resize(im, 0.25, &vips.ResizeOptions{
    Kernel: vips.Ptr(vips.KernelNearest),                        // KernelNearest is 0
})
```

Regenerate against whatever libvips you have with `make generate`.

### The generic layer

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

### Metadata, sources and targets

```go
im.Fields()                    // every field the loader attached
im.Orientation()               // EXIF orientation, 1 when absent
im.EXIF()                      // every exif-* field as text
im.Profile()                   // embedded ICC profile
im.HasAlpha(), im.Pages(), im.Resolution()

own, _ := vips.Copy(im, nil)   // take a private header before mutating
own.SetString("comment", "mine")
own.RemoveField("exif-data")
```

The copy matters. libvips caches built operations, so two callers asking for the
same thing get the same object back; mutating that header from more than one
goroutine corrupts the field list.

Streaming goes through sources and targets:

```go
src, _ := vips.NewSourceFromFile("in.jpg")
im, _ := vips.JpegloadSource(src, nil)

target, _ := vips.NewTargetToMemory()
_ = vips.WebpsaveTarget(im, target, nil)
buf, _ := target.Bytes()
```

### Runtime controls

```go
vips.SetConcurrency(4)     // worker threads per operation
vips.SetCacheMax(100)      // operations kept for reuse
vips.ClearCache()
vips.Memory()              // libvips' own allocation counters
```

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
index page, labelling each with the govips method it stands in for. The rendered
result is checked in under `site/`:

```bash
make site                                    # regenerate from site/source.png
make site SITE_SOURCE=/path/to/photo.jpg     # or from your own
open site/index.html
```

`site/` carries a `go.mod` of its own, which is not an accident. The go command
treats a directory containing one as a separate module and leaves it out of the
parent's module zip, so `go get` on this repository fetches 41 files and about
630 KB with none of the demonstration images among them. `make check-module-size`
fails if that ever stops being true.

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

### Memory

Reference counting across cgo is the part of this that cannot be made correct by
construction, so `internal/soak` watches libvips' own counters — Go's heap
profiler cannot see a byte of it. Two hundred serial rounds of a full pipeline,
eight goroutines running it concurrently, four hundred close-then-collect cycles
and five hundred failed calls all have to finish with the byte count, the live
allocation count and the descriptor count exactly where they started.

```bash
make soak
make asan       # Linux only, the Go toolchain has no -asan on darwin/arm64
make valgrind   # Linux
```

This has already paid for itself twice. It caught a use-after-free where `Close`
and the collector both released the same reference, and it caught
`vips_cache_drop_all` leaking one operation and one pixel buffer per call
afterwards — in libvips 8.18 that function destroys the cache table and nothing
re-creates it, so `ClearCache` here shrinks the limit and restores it instead.

### CI

`.github/workflows/ci.yml` runs the whole set against three real libvips
versions, since "one binding, any version" is worth nothing untested: 8.12 on
ubuntu-22.04, 8.15 on ubuntu-24.04 and 8.18 on macOS. A separate job regenerates
the typed layer against the CI libvips and requires the result to build and pass,
and a third runs ASan and Valgrind over the soak suite.

## Status

Not ready for production. The design is verified but the mileage is not: this
has run on one machine against one libvips version, with no external review and
no production hours, against govips' years of service and vipsgen's use inside
imagor. The CI matrix above is written and unexecuted until this repository has a
remote.

What is done: the generic call path, the generated typed layer, metadata,
sources and targets, the differential oracle, and the leak suite. What is left
before anyone should ship it: green CI on Linux, ASan and Valgrind actually run
rather than merely configured, and `io.Reader`/`io.Writer` streaming, which needs
callbacks into Go and is not the same job as the file and memory sources here.

## License

MIT
