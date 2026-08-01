# Security

## Reporting

Report a vulnerability through GitHub's private advisory form:
<https://github.com/kirklin/vipsx/security/advisories/new>. Please do not open a
public issue for something exploitable.

This is a small project with one maintainer, so the honest commitment is a
modest one: acknowledgement within seven days, and an assessment within thirty.
If a report needs faster handling than that, say so in it.

## What is in scope

vipsx is a binding. Most of the code that touches an image is libvips and the
decoders under it, so the split matters:

**Report here** anything wrong with the way this package crosses the boundary:
a reference miscounted, memory freed twice or not at all, a buffer measured
wrongly on its way in or out, a Go value marshalled as the wrong C type, a
handle used after it was released.

**Report to [libvips](https://github.com/libvips/libvips/security)** anything
where a crafted image makes libvips itself misbehave. A decoder that reads past
the end of a buffer is theirs, not this package's — but if you are not sure
which side a finding lands on, send it here and it will be forwarded.

## Handling untrusted images

If this package is decoding images from people you do not control, three
switches exist for it. None is on by default, because a library that quietly
narrows what a program can open is its own kind of surprise.

```go
// Off with the loaders libvips itself marks untrusted.
vips.BlockUntrusted(true)

// Or name what you serve, which is stricter: nothing loads, then these do.
vips.BlockOperation("VipsForeignLoad", true)
vips.BlockOperation("VipsForeignLoadJpeg", false)
vips.BlockOperation("VipsForeignLoadPng", false)
vips.BlockOperation("VipsForeignLoadWebp", false)

// Cap what an unseekable stream can buffer. libvips defaults to 1 GB, which is
// a limit rather than a choice.
vips.SetPipeReadLimit(64 << 20)
```

Two more things belong in the same setup, because decode time and memory are
attacker-controlled even when the decoder is sound:

```go
// A deadline. libvips has none of its own; without this a single image can
// hold a worker for as long as it takes.
w, _ := im.CancelOn(ctx)
defer w.Stop()

// Bound the cache. It is a cache, so it will use what it is given.
vips.SetCacheMaxMem(512 << 20)
vips.SetCacheMaxFiles(100)
```

Checking dimensions before doing the work is worth more than any of the above,
since the header is cheap and the pixels are not:

```go
if im.Width()*im.Height() > maxPixels {
    return errTooLarge
}
```

## Keeping libvips current

The decoders are where the CVEs are, and they are libvips' dependencies rather
than this package's: libwebp, libtiff, libjpeg-turbo, libheif and the rest.
Nothing here pins or vendors them — the version in use is whatever the system
provides at build time. Watching your distribution's security updates for
`libvips` and its dependencies does more for a deployment than watching this
repository.

`vips.Version()` reports what a running process is actually linked against,
which is the number to log and the number to check against an advisory.
