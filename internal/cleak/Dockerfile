# A Linux box for the leak check, because no development machine here can run it.
#
# LeakSanitizer is not available on darwin/arm64 at all, and on the hosted CI
# runner it reports nothing even for a program that is only a leak. A container
# can, so this is where the last uncovered part of the C core gets looked at.
#
# Debian 12 rather than something newer on purpose: it carries libvips 8.14,
# which is the oldest version this binding supports, so the run doubles as a
# check of the floor.
#
#   make docker-cleak
#
# gcc, not clang: Debian ships the AddressSanitizer runtime with gcc, and its
# clang-14 package has no aarch64 runtime to link against. No extra container
# privileges are needed — ptrace was the suspected reason the CI runner fails,
# and this run disproves it, since it works under the default seccomp profile.

FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
        libvips-dev libvips-tools pkg-config clang make \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY . .

ENV CGO_CFLAGS_ALLOW=-Xpreprocessor

CMD ["sh", "-c", "\
    echo '=== libvips ===' && pkg-config --modversion vips && \
    echo && echo '=== leak check over the C core ===' && make cleak CLEAK_CC=gcc && \
    echo && echo '=== the Go suites, on this libvips ===' && \
    go test ./vips/ ./internal/coverage/ ./internal/soak/ -count=1 && \
    go test ./internal/difftest/ -count=1 -timeout 30m \
"]
