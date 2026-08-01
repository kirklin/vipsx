# libvips headers use -Xpreprocessor, which cgo refuses to pass on unless told.
export CGO_CFLAGS_ALLOW = -Xpreprocessor

.PHONY: all check test race cover diff soak bigdata asan cleak docker-cleak generate site check-module-size check-bootstrap fmt vet ci

all: check fmt vet test

check:
	@pkg-config --exists vips || \
		(echo "libvips not found: brew install vips, or apt install libvips-dev" && exit 1)
	@echo "libvips $$(pkg-config --modversion vips)"

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./vips/ -count=1

race:
	go test ./vips/ -race -count=1

# Coverage of what govips and vipsgen expose.
cover:
	go test ./internal/coverage/ -count=1 -v

# The oracle. Set VIPSX_IMAGE_DIR to compare on your own images too.
diff:
	go test ./internal/difftest/ -count=1 -timeout 30m

# Leak and lifetime checks against libvips' own allocation counters.
soak:
	go test ./internal/soak/ -count=1 -timeout 20m

# Fetches the large NASA fixtures, which are too big to keep in the repository.
# One 21600x21600 tile in two formats, about 600 MB together, resumable. See
# the script for why those two and not more.
BIGDATA_DIR ?= $(HOME)/.cache/vipsx-images
bigdata:
	./internal/soak/fetch-bigdata.sh "$(BIGDATA_DIR)"

# A leak check for the C core, with no Go runtime to confuse the checker.
#
# Go's -asan does not run LeakSanitizer, which was established by deliberately
# losing two thousand allocations and watching the run report nothing. This
# builds the same C sources into a plain program instead, where the checker
# works. --leak first, to prove the checker is on before believing it.
# Which compiler builds the sanitised program. clang by default, since that is
# what macOS has; Debian ships the AddressSanitizer runtime with gcc instead, so
# the container overrides this rather than installing a second toolchain.
CLEAK_CC ?= clang
CLEAK_CFLAGS = -g -O1 -fsanitize=address -fno-omit-frame-pointer -Ivips

# Everything that does not call back into Go.
#
# A file including _cgo_export.h cannot be compiled without cgo having run, and
# nothing in one is reachable from a program with no Go in it anyway: those
# handlers exist to hand bytes to an io.Reader or a report to a callback. The
# list is derived rather than written down, because writing it down is how it
# went stale — stream.c was named here, and adding logging.c and progress.c
# broke the build on CI rather than at the desk of whoever added them.
CLEAK_SOURCES = $(shell grep -L "_cgo_export.h" vips/*.c)
cleak:
	@which $(CLEAK_CC) >/dev/null || \
		(echo "$(CLEAK_CC) is needed for -fsanitize=address" && exit 1)
	$(CLEAK_CC) $(CLEAK_CFLAGS) $$(pkg-config --cflags vips) \
		$(CLEAK_SOURCES) internal/cleak/main.c \
		$$(pkg-config --libs vips) -o /tmp/vipsx-cleak
	@echo "--- can this machine detect leaks at all? ---"
	@ASAN_OPTIONS=detect_leaks=1 LSAN_OPTIONS=exitcode=23 \
		/tmp/vipsx-cleak --leak >/tmp/vipsx-cleak-probe.log 2>&1; \
	probe=$$?; \
	if [ $$probe -eq 0 ] || ! grep -q "LeakSanitizer" /tmp/vipsx-cleak-probe.log; then \
		echo "NO. A deliberate leak was not reported as a leak, so leak checking"; \
		echo "is off here. What the probe said:"; \
		grep -m2 -iE "leak|not supported" /tmp/vipsx-cleak-probe.log || true; \
		echo "AddressSanitizer still ran: invalid reads, writes and double frees"; \
		echo "are covered below, leaks are not. Run this on a machine where"; \
		echo "LeakSanitizer works to get the rest."; \
		echo "--- AddressSanitizer over the C core ---"; \
		/tmp/vipsx-cleak; \
	else \
		echo "YES. Leak checking is active and proven; requiring a clean run."; \
		echo "--- AddressSanitizer and LeakSanitizer over the C core ---"; \
		LSAN_OPTIONS=suppressions=$(PWD)/.github/lsan.supp:exitcode=23 \
			/tmp/vipsx-cleak; \
	fi

# The leak check on Linux, in a container, which is the only place it runs.
docker-cleak:
	docker build -f internal/cleak/Dockerfile -t vipsx-cleak .
	docker run --rm vipsx-cleak

# Linux only: the Go toolchain has no -asan on darwin/arm64.
asan:
	CC=clang ASAN_OPTIONS=detect_leaks=0 \
		go test -asan -count=1 -timeout 30m ./vips/ ./internal/soak/

# Rebuild the demonstration page in site/.
#
# site/ carries its own go.mod, which is what keeps its images out of the module
# zip. Anyone running `go get` on this repository should not be paying to
# download screenshots.
# The source lives in the repository so that regenerating needs nothing but a
# checkout. It is in site/ rather than testdata/ for the same reason as the
# rendered images: site/ is a separate module and none of it ships to consumers.
SITE_SOURCE ?= site/source.png
site:
	rm -f site/[0-9]*.jpg site/index.html
	go run ./examples/gallery -width 600 -format .jpg -q 90 "$(SITE_SOURCE)" site
	@test -f site/go.mod || (echo "site/go.mod is missing; without it the images ship to every consumer" && exit 1)

# The hand-written core must compile with the generated layer absent.
#
# The generator imports this package to ask libvips what to generate, so if
# anything hand-written calls a generated function, the package stops compiling
# the moment the generated files are removed — which is exactly when the
# generator is about to write them. Strip did this once, by calling Copy.
# The generated files are parked in a fresh mktemp directory and restored by a
# trap, so an interrupt cannot strand the only copy, and two checkouts running
# this at once cannot fight over a fixed path.
check-bootstrap:
	@tmp=$$(mktemp -d); \
	cp vips/zz_generated_*.go "$$tmp"/; \
	trap 'cp "$$tmp"/zz_generated_*.go vips/ 2>/dev/null; rm -rf "$$tmp"' EXIT; \
	rm -f vips/zz_generated_*.go; \
	if go build ./vips/ 2>"$$tmp"/bootstrap.log; then \
		echo "the core compiles without the generated layer"; \
	else \
		echo "the core does not compile without the generated layer:"; \
		grep -m5 "undefined" "$$tmp"/bootstrap.log || cat "$$tmp"/bootstrap.log; \
		echo "something hand-written is calling generated code, which leaves the"; \
		echo "generator unable to run."; \
		exit 1; \
	fi

# Fails if the demonstration images have leaked into the module.
check-module-size:
	@test -f site/go.mod || (echo "site/go.mod is missing" && exit 1)
	@if go list ./... | grep -q '/site'; then \
		echo "site is part of the module; its images would ship to consumers"; \
		exit 1; \
	fi
	@echo "site/ is a separate module and stays out of the module zip"

# Rebuild the typed layer from the installed libvips.
generate:
	rm -f vips/zz_generated_*.go
	go run ./cmd/vipsx-gen -out vips
	gofmt -w vips

# Everything CI runs, minus the sanitizers.
ci: check vet test cover race diff soak check-module-size check-bootstrap
	@test -z "$$(gofmt -l .)" || (echo "not gofmt-clean:"; gofmt -l .; exit 1)
	@echo "all green"
