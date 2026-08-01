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

## Version

`vips.PackageVersion` carries the release this source is, and
`vips.ModuleVersion()` reports what the calling program actually resolved from
the module graph — the honest answer when a consumer is pinned to something
older. `vips.Version()` is a different question: it reports the libvips this is
linked against.

The constant and the git tag are checked against each other by CI on every
tagged build, because a version file that can quietly disagree with the tag is
worse than not having one.

## Install

Needs libvips 8.14 or newer, and a C toolchain.

The floor is where it is because CI tests it there: Debian 12 carries 8.14 and is
supported into 2028. 8.12 was tried and dropped — it builds after a little
conditional compilation, then dies with a stack smash inside libvips partway
through the differential suite, and the only common distribution still shipping
it, Ubuntu 22.04, leaves support in April 2027.

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
im.EXIF()                      // the EXIF tags, as libvips renders them
im.Profile()                   // embedded ICC profile
im.HasAlpha(), im.Pages(), im.Resolution()

own, _ := vips.Copy(im, nil)   // take a private header before mutating
own.SetString("comment", "mine")
own.RemoveField("exif-data")
```

The copy matters. libvips caches built operations, so two callers asking for the
same thing get the same object back; mutating that header from more than one
goroutine corrupts the field list.

Removing metadata is its own step, because two of the fields change how the
result looks and neither is obvious:

```go
fields := im.MetadataFields()   // EXIF, XMP, IPTC, ICC, orientation, the rest
own, err := im.Strip()          // all of them, on a copy
own, err := im.Strip("exif-data", "xmp-data")   // or only these
```

On a photograph from a phone, that is the difference between shipping the
camera's serial number, the lens model and the timestamp to whoever downloads
the image, and not. On one 800px JPEG here it also took 64 KB down to 35 KB.

`Strip` copies rather than mutating, for the reason above: an image from an
operation may be shared. It refuses fields that describe the pixels — width,
interpretation and the like — rather than failing obscurely inside libvips.

Two of its removals need care. Dropping `orientation` does not straighten
anything: a phone stores the photograph sideways with a tag saying which way up
it goes, so losing the tag leaves it sideways for good. Call `Autorot` first,
which applies the rotation and then clears the tag. Dropping
`icc-profile-data` changes the colours of anything not already in sRGB, because
the numbers stay and the note explaining them goes.

### Streaming

Sources and targets read and write without materialising the whole image. They
can be a file, a byte slice, memory, or any `io.Reader` and `io.Writer`:

```go
src, _ := vips.NewSourceFromReader(req.Body)   // no temporary file
defer src.Close()
im, _ := vips.LoadSource(src)                  // sniffs content, like LoadFile

target, _ := vips.NewTargetToWriter(w)         // straight to the response
defer target.Close()
_ = vips.SaveTarget(im, target, ".webp", vips.In("Q", 82))
if err := target.Err(); err != nil { ... }     // what the writer said
```

A reader that also seeks is used as one, and libvips reads the file the way it
would a real one. A reader that cannot — an HTTP body, a pipe — makes libvips
take its sequential path and buffer what it needs instead. Both work; the
seekable one works for more formats, since a few loaders cannot operate without
seeking.

`LoadSource` picks the loader by sniffing, the same way `LoadFile` does, so the
format does not have to be known up front — which on a request body is where it
is least available. `LoaderForSource` and `SaverForTarget` report the choice
without making it.

`Close` releases the reader or writer on the spot, so it comes after the save:
evaluate every image loaded from the source first, then close. Demanding bytes
after Close fails the operation cleanly — a file-backed source is more
forgiving there, since libvips holds the file itself. Skipping Close pins the
reader until the collector gets to the handle; `vips.OpenStreams()` reports how
many are outstanding, returns to zero when everything is closed, and the soak
suite asserts exactly that. `Err` reports what the reader or writer actually
said when a call failed — libvips' own error says only that reading or writing
failed — and it keeps answering after Close.

`CopyMemory` is the way out of that ordering when it does not suit. It renders
the pipeline into memory, which cuts the image's tie to the source, so the
reader can go back to its owner before anything is saved:

```go
own, _ := im.CopyMemory()   // pixels are here now
src.Close()                 // and the body can go back
```

### Raw pixels

For pixels that did not come from a file — another library's decoder, a
framebuffer, a Go `image.Image`:

```go
im, _ := vips.NewImageFromMemory(pix, w, h, 3, im.Format())
out, _ := im.WriteToMemory()   // band-packed rows, not an encoding
```

libvips takes its own copy on the way in, so the slice is free immediately. The
non-copying constructor is deliberately not exposed: it keeps the caller's
pointer for the life of the image, and Go memory belongs to a collector that
may move or reclaim it.

### Deadlines and cancellation

libvips has no notion of a deadline. What it has is progress reporting and a
kill flag, and tying the two to a `context.Context` is what turns them into one:

```go
w, _ := im.CancelOn(ctx)
defer w.Stop()

buf, err := vips.SaveBuffer(im, ".webp")
if err != nil {
    if cause := w.Err(); cause != nil {
        return cause          // context.DeadlineExceeded
    }
    return err                // "VipsImage: killed for image temp-58"
}
```

`w.Err()` is the point. libvips reports a killed pipeline as a generic failure,
which says that a pipeline stopped and nothing about why; the reason lives on
the watch. Cancellation is noticed at the next progress report, so it is prompt
rather than instant — measured here, a 60 ms deadline on a 600 ms pipeline lands
within a millisecond or two.

`OnProgress` is the general form, for a progress bar or any other reason to stop:

```go
w, _ := im.OnProgress(func(p vips.Progress) error {
    if p.Total > tooManyPixels {
        return errTooBig
    }
    return nil                // or non-nil to stop the evaluation
})
```

### Hardening

For a process that decodes images it did not choose:

```go
vips.BlockUntrusted(true)                   // off with the loaders libvips distrusts
vips.SetPipeReadLimit(64 << 20)             // cap what an unseekable stream can buffer

vips.BlockOperation("VipsForeignLoad", true)        // nothing loads
vips.BlockOperation("VipsForeignLoadJpeg", false)   // except what you serve
vips.BlockOperation("VipsForeignLoadPng", false)
```

Names here are libvips class names rather than operation nicknames, and blocking
covers a class and everything under it, which is what makes the deny-all shape
one call rather than thirty.

### Runtime controls

```go
vips.SetConcurrency(4)     // worker threads per operation
vips.SetCacheMax(100)      // operations kept for reuse
vips.SetCacheMaxMem(1<<30) // ... or by bytes
vips.SetCacheMaxFiles(100) // ... or by descriptors held open
vips.ClearCache()
vips.Memory()              // libvips' own allocation counters
```

libvips complains through GLib, which writes to stderr — in a service, the one
place nobody reads. `SetLogHandler` puts those lines with the rest of the logs:

```go
vips.SetLogHandler(func(domain string, level vips.LogLevel, msg string) {
    slog.Warn("libvips", "domain", domain, "level", level.String(), "msg", msg)
})
```

### Handles, and using one after Close

`Close` on an image, source or target releases it, and using it afterwards
panics with `*vips.ClosedError` naming the method. That is deliberate: a closed
handle used to reach libvips as a NULL, which it dereferences, so the mistake
was a SIGSEGV that took the process down and named neither the image nor the
caller. The pointer is read and cleared atomically, so a `Close` racing a read
is a defined panic rather than a read of freed memory.

Passing a closed handle as an *argument* is an error rather than a panic, since
a call is a value the caller is expected to check.

### Errors under concurrency

libvips keeps one error buffer for the whole process, not one per thread or per
call. Draining it here is atomic, so nothing is lost, but two operations failing
at the same moment append to the same place: a message can arrive carrying
another goroutine's text. Measured with eight goroutines failing repeatedly,
88% of messages did not name their own call.

Nothing in a binding fixes that while calls run concurrently — pyvips and govips
read the same buffer the same way. `vips.SetErrorIsolation(true)` serialises
operations, which takes that number to zero, and is meant for reproducing a
problem rather than for serving traffic.

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
`internal/difftest` invokes each operation twice — once through this binding,
once through the `vips` command line — and requires the pixels to match exactly.
Both sides are built from one set of argument values, so the two cannot drift
into testing different things.

```bash
go test ./internal/difftest/                                   # about 45s
VIPSX_IMAGE_DIR=/path/to/photos go test ./internal/difftest/   # adds real images
```

On libvips 8.18 that is 221 comparisons across two passes: one sending only
required arguments, and one adding every optional argument the command line can
express. The second pass is the only reason the boolean and flags paths are
exercised at all, since almost none of those are required arguments. Save
operations are compared by the bytes they wrote rather than by an image they
returned.

Counting operations flatters this, so the suite reports the number that matters
instead: how many of the eighteen ways a Go value can become a libvips argument
have been checked against something this package did not write. Sources, targets
and buffers cannot be handed to a command line at all, so `TestStreamsAgainstCLI`
pairs each stream operation with its file-based sibling — the CLI reads the file,
the binding reads the same bytes through a source or a buffer, and the results
must agree. That leaves four marginal kinds unverified: `uint64`, `refstring`,
`[]int`, and the generic object fallback, together about twenty argument slots in
all of libvips.

Both sides run pinned to one worker thread, and neither is incidental. Several
libvips operations reduce over the whole image with one accumulator per thread
and combine them in completion order, so with threads free the last bit of the
answer depends on scheduling: `stats` over the same file gives the command line
eight different answers in ten runs. Two implementations cannot be compared for
exactness while the implementation is free to disagree with itself, so the
comparison removes that freedom rather than tolerating the result.

What one thread does not fix is FFTW, which picks its algorithm at run time and
does not pick the same one in every process. `phasecor` disagrees across
processes about one run in ten, by one unit after rounding to eight bits, and
re-sampling the command line ten times per mismatch still misses it. Those
operations carry a named one-unit allowance with that measurement written next
to it. Everything else is required to match to the bit.

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
make cleak      # leak check the C core; needs clang
make bigdata    # fetch the large fixtures those counters are worth watching over
```

The soak pipeline runs on a 320x240 synthetic image, which is the right size for
counting references two hundred times over but reaches nothing that only goes
wrong at scale. `make bigdata` fetches one public-domain NASA tile for that, in
two formats — 21600x21600, 274 MB as PNG and 306 MB as GeoTIFF — into
`~/.cache/vipsx-images`, which is also what `VIPSX_IMAGE_DIR` wants pointing at.

Two files rather than a set, because the formats are what differ and the pixels
are not. A PNG has no random access and forces the whole image to be held at
once; a GeoTIFF can be read a region at a time. Those are different paths
through the loader, and a second tile would only be a second crop of the same
planet at the same resolution. `TILES="A1 C1"` pulls more for anyone who wants
them. They stay outside the checkout for the reason `site/` does, and the
download resumes, so an interrupted run costs only what it had not yet fetched.

Leak checking took three attempts to get working, and the first two failures are
worth recording because both look like a clean bill of health from outside.

`go test -asan` does not run LeakSanitizer: a probe that deliberately lost two
thousand allocations under `detect_leaks=1` reported nothing and exited zero, and
[golang/go#67833](https://github.com/golang/go/issues/67833), the proposal to
make it usable, is still open. Valgrind cannot read a Go binary either — Go's
assembly string routines read past the end of short C strings on purpose, its
concurrent collector confuses the memory model, and its preemption signals
collide with Valgrind's own.

So `make cleak` builds the same C sources into a plain program with no Go runtime
and checks that. Under clang on the CI runner that reported nothing too, even for
a program containing only a leak; under gcc, on the same runner, it works. The
cause was the toolchain, not the environment — an earlier version of this file
blamed the runner's ptrace restrictions, which a container running under the
default seccomp profile disproved.

Because two of those three failures were silent, the target proves itself before
it reports anything: it leaks a hundred allocations on purpose, requires the
checker to catch them, and only then requires a clean run. Where no leak checker
is available at all it says so and runs the rest of AddressSanitizer — invalid
reads and writes, double frees, use after free — over fifty rounds through every
allocating path in the C core.

```bash
make cleak          # needs a leak checker to do the leak half; says which it did
make docker-cleak   # Debian 12 container, where both halves run
make soak           # libvips' own counters, from Go
```

Between them: `internal/soak` watches libvips' allocation counters across 200
serial and 320 concurrent rounds, which covers everything libvips allocates, and
`make cleak` covers this package's own `malloc` and `strdup` calls, which those
counters cannot see.

### CI

`.github/workflows/ci.yml` runs the whole set against three real libvips
versions, since "one binding, any version" is worth nothing untested: 8.14 in a
Debian 12 container, 8.15 on ubuntu-24.04, and 8.18 on macOS. The oldest is
tested in a container because no runner image ships it, and testing the floor is
the only thing that makes the floor a fact rather than a hope. A separate job
regenerates the typed layer against the CI libvips and requires the result to
build and pass, and a third runs ASan and Valgrind over the soak suite.

For comparison: govips states 8.14+ but its CI exercises a single runner image,
and vipsgen ships pre-generated packages for 8.16, 8.17 and 8.18, so the import
path has to change with the installed version.

## Status

Not ready for production, and the reason is mileage rather than surface. This
has no production hours behind it, against govips' years of service and
vipsgen's use inside imagor. No amount of code closes that gap; only running it
does.

What is done: the generic call path, the generated typed layer, metadata,
`io.Reader`/`io.Writer` streaming with content sniffing, raw pixels in and out,
deadlines and cancellation, the hardening switches, the differential oracle and
the leak suite.

The CI matrix does run, and is green: libvips 8.14 in a Debian 12 container,
8.15 on ubuntu-24.04, 8.18 on macOS, plus regenerating the typed layer against
the CI libvips, ASan, and the C leak check. "One binding, any version" is
tested rather than asserted.

What is left before anyone should ship it:

- **A stability promise.** This is v0, so the API can still move. v1, and a
  statement about what v1 means, is worth more to a prospective user than
  another function.
- **Hours.** Somebody's real traffic, for long enough that the numbers mean
  something, with `vips.Memory()` watched across it. This is the whole of the
  gap against govips and vipsgen, and no amount of code closes it.

Two limitations are properties of libvips rather than gaps here, and are
documented above rather than listed as work: error messages cannot be attributed
under concurrency without serialising calls, and cancellation is noticed at the
next progress report rather than instantly.

## License

MIT
