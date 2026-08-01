# vipsx

[![Go Reference](https://pkg.go.dev/badge/github.com/kirklin/vipsx.svg)](https://pkg.go.dev/github.com/kirklin/vipsx)
[![CI](https://github.com/kirklin/vipsx/actions/workflows/ci.yml/badge.svg)](https://github.com/kirklin/vipsx/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Go bindings for [libvips](https://www.libvips.org/), derived from the installed
library at runtime.

English | [简体中文](README.zh-CN.md)

## Overview

libvips describes its own operations through the GObject type system. vipsx reads
that description at runtime rather than wrapping operations individually, so a
single call path covers every operation the installed libvips exposes, including
operations added after this package was released.

Two APIs sit on that call path:

- **Typed API** — one function and one options struct per operation, generated
  from the installed libvips. On 8.18 this is 330 functions and 47 enum types,
  entirely in Go: no generated C, and no per-operation cgo to compile.
- **Generic API** — `Call`, which accepts any operation name and resolves its
  signature at runtime. Used directly for operations the generator has not been
  run against.

## Requirements

- libvips 8.14 or later
- Go 1.24 or later
- cgo enabled, with a C toolchain

The libvips floor is set by continuous integration rather than by inspection:
Debian 12 ships 8.14 and is supported until 2028.

## Installation

```bash
brew install vips        # or: apt install libvips-dev
go get github.com/kirklin/vipsx
```

On macOS, cgo must be permitted to pass through the preprocessor flag libvips
uses:

```bash
export CGO_CFLAGS_ALLOW=-Xpreprocessor
```

## Documentation

API reference: [pkg.go.dev/github.com/kirklin/vipsx](https://pkg.go.dev/github.com/kirklin/vipsx)

## Usage

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

### Typed API

```go
small, err := vips.Resize(im, 0.5, nil)
blur, err := vips.Gaussblur(small, 2.0, nil)
gray, err := vips.Colourspace(blur, vips.InterpretationBW, nil)
avg, err := vips.Avg(gray)
```

Optional arguments are pointer fields, so an argument that was not supplied is
distinguishable from one explicitly set to zero. In libvips 8.18, 357 optional
arguments have a non-zero default, where the two cases produce different results.

```go
vips.Resize(im, 0.5, &vips.ResizeOptions{Kernel: vips.Ptr(vips.KernelNearest)})
```

Optional outputs are requested by pointing a field at a destination variable.
Fields left nil are not requested, and their destinations are not written.

```go
var x, y int
max, err := vips.Max(im, &vips.MaxOptions{X: &x, Y: &y})
```

### Generic API

Argument names are libvips' own, as reported by `vips <operation>`.

```go
outs, err := vips.Call("gaussblur", vips.In("in", im), vips.In("sigma", 3.0))
im, err := outs.Image("out")
```

`Out` requests an optional output. `Describe`, `Operations` and `EnumValues`
report the signatures, operation list and enum members of the installed library.

### Loading and saving

Input format is detected from content; output format is selected by extension.

```go
im, err := vips.LoadFile("in.heic")
buf, err := vips.SaveBuffer(im, ".jpg", vips.In("Q", 90))
```

Sources and targets stream from an `io.Reader` and to an `io.Writer` without an
intermediate file.

```go
src, _ := vips.NewSourceFromReader(req.Body)
defer src.Close()
im, _ := vips.LoadSource(src)

target, _ := vips.NewTargetToWriter(w)
defer target.Close()
err = vips.SaveTarget(im, target, ".webp", vips.In("Q", 82))
if err := target.Err(); err != nil {
    // the error reported by the writer, rather than the generic libvips message
}
```

## Semantics

### Image sharing

libvips caches built operations, so two identical calls may return handles to the
same underlying image. Concurrent reads are safe. Modifying an image header is
not: the change is visible to every holder. Copy before modifying.

```go
own, _ := vips.Copy(im, nil)
own.SetString("comment", "mine")
```

Operations that modify their input in place — the draw family — are handled by
the binding. `Call` substitutes a private copy and returns it, so the argument
supplied by the caller is never modified.

```go
drawn, err := vips.DrawRect(im, []float64{255, 0, 0}, 60, 60, 300, 200, nil)
```

### Handle lifetime

An image holds one libvips reference, released by `Close` or by the garbage
collector. Use after `Close` panics with `*ClosedError` rather than dereferencing
freed memory.

Handles are safe to use concurrently, `Close` included. Each method acquires its
own reference before entering C, so a `Close` racing another call either follows
a completed call or causes that call to panic with `*ClosedError`.

An image is a lazily evaluated pipeline rather than a buffer of pixels, so a
streaming source must remain open until the image is evaluated. `CopyMemory`
materialises the pixels and releases the dependency.

### Cancellation

libvips has no deadline mechanism. `CancelOn` terminates the pipeline at its next
progress report, and reports the cause.

```go
w, _ := im.CancelOn(ctx)
defer w.Stop()

if _, err := vips.SaveBuffer(im, ".webp"); err != nil {
    if cause := w.Err(); cause != nil {
        return cause    // context.DeadlineExceeded, rather than a generic failure
    }
    return err
}
```

## Security

For processes decoding untrusted input:

```go
vips.BlockUntrusted(true)                          // block loaders libvips marks untrusted
vips.BlockOperation("VipsForeignLoad", true)       // or block all loaders,
vips.BlockOperation("VipsForeignLoadJpeg", false)  // then permit specific formats
vips.SetPipeReadLimit(64 << 20)                    // cap buffering of unseekable input
```

See [SECURITY.md](SECURITY.md) for the supported configuration and reporting
process.

## Testing

The binding contains no per-operation code, so correctness is established by
differential testing rather than by review. `internal/difftest` executes each
operation through this binding and through the `vips` command line, constructing
both invocations from one set of argument values, and requires the results to
match exactly. Both sides run pinned to a single worker thread, since several
libvips reductions are not bit-reproducible across thread counts.

| Target | Scope |
|---|---|
| `make test` | Unit tests |
| `make race` | Unit tests under the race detector |
| `make diff` | Differential comparison against the `vips` command line |
| `make soak` | libvips allocation counters over 200 serial and 320 concurrent rounds |
| `make cleak` | AddressSanitizer over the C core; reports whether leak detection was available |
| `make cover` | Every operation exposed by govips and vipsgen must be reachable |

Continuous integration runs the suite against libvips 8.14 (Debian 12 container),
8.15 (ubuntu-24.04) and 8.18 (macOS). A separate job regenerates the typed API
against the installed library and requires the result to build and pass tests.

## Compatibility

The typed API is generated from the installed libvips, so its surface varies with
the version in use; the generic API does not. Releases follow semantic
versioning, with the pre-1.0 caveat that minor versions may introduce breaking
changes. `vips.PackageVersion` reports the release, `vips.ModuleVersion` the
version resolved by the module graph, and `vips.Version` the libvips linked at
runtime.

## Comparison

Operation coverage is verified by `internal/coverage`, which requires every
operation exposed by govips and vipsgen to be reachable here.

| Binding | Operations reached |
|---|---|
| vipsx | 330 |
| vipsgen | 289 |
| govips | 185 |

vipsx differs from both in deriving its surface at runtime rather than shipping
generated bindings per libvips version.

`examples/gallery` renders 35 operations against a photograph and writes an index
page. The output is committed under [site/](site/), which carries a separate
`go.mod` so that the images are excluded from the module archive.

## License

MIT — see [LICENSE](LICENSE).
