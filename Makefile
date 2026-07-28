# libvips headers use -Xpreprocessor, which cgo refuses to pass on unless told.
export CGO_CFLAGS_ALLOW = -Xpreprocessor

.PHONY: all check test race cover diff soak asan cleak generate site check-module-size fmt vet ci

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

# A leak check for the C core, with no Go runtime to confuse the checker.
#
# Go's -asan does not run LeakSanitizer, which was established by deliberately
# losing two thousand allocations and watching the run report nothing. This
# builds the same C sources into a plain program instead, where the checker
# works. --leak first, to prove the checker is on before believing it.
CLEAK_CFLAGS = -g -O1 -fsanitize=address -fno-omit-frame-pointer -Ivips
cleak:
	@which clang >/dev/null || (echo "clang is needed for -fsanitize=address" && exit 1)
	clang $(CLEAK_CFLAGS) $$(pkg-config --cflags vips) \
		vips/*.c internal/cleak/main.c \
		$$(pkg-config --libs vips) -o /tmp/vipsx-cleak
	@if ASAN_OPTIONS=detect_leaks=1 /tmp/vipsx-cleak --leak 2>&1 | \
		grep -q "detect_leaks is not supported"; then \
		echo "no LeakSanitizer on this platform: AddressSanitizer ran, leak checking did not"; \
		/tmp/vipsx-cleak; \
	else \
		echo "--- proving the leak checker is active ---"; \
		if LSAN_OPTIONS=suppressions=$(PWD)/.github/lsan.supp \
			/tmp/vipsx-cleak --leak >/dev/null 2>&1; then \
			echo "leak detection is not active: a deliberate leak went unreported"; \
			exit 1; \
		fi; \
		echo "checker confirmed active"; \
		echo "--- the real run ---"; \
		LSAN_OPTIONS=suppressions=$(PWD)/.github/lsan.supp /tmp/vipsx-cleak; \
	fi

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

# Fails if the demonstration images have leaked into the module.
check-module-size:
	@test -f site/go.mod || (echo "site/go.mod is missing" && exit 1)
	@go list ./... | grep -q '/site' && \
		(echo "site is part of the module; its images would ship to consumers" && exit 1) || true
	@echo "site/ is a separate module and stays out of the module zip"

# Rebuild the typed layer from the installed libvips.
generate:
	rm -f vips/zz_generated_*.go
	go run ./cmd/vipsx-gen -out vips
	gofmt -w vips

# Everything CI runs, minus the sanitizers.
ci: check vet test cover race diff soak check-module-size
	@test -z "$$(gofmt -l .)" || (echo "not gofmt-clean:"; gofmt -l .; exit 1)
	@echo "all green"
