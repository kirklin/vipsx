# libvips headers use -Xpreprocessor, which cgo refuses to pass on unless told.
export CGO_CFLAGS_ALLOW = -Xpreprocessor

.PHONY: all check test race cover diff soak asan valgrind generate fmt vet ci

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

# Linux only: the Go toolchain has no -asan on darwin/arm64.
asan:
	CC=clang ASAN_OPTIONS=detect_leaks=0 \
		go test -asan -count=1 -timeout 30m ./vips/ ./internal/soak/

valgrind:
	go test -c -o /tmp/vipsx-soak.test ./internal/soak/
	valgrind --error-exitcode=42 --leak-check=full \
		--show-leak-kinds=definite --errors-for-leak-kinds=definite \
		--suppressions=.github/valgrind.supp \
		/tmp/vipsx-soak.test -test.short

# Rebuild the typed layer from the installed libvips.
generate:
	rm -f vips/zz_generated_*.go
	go run ./cmd/vipsx-gen -out vips
	gofmt -w vips

# Everything CI runs, minus the sanitizers.
ci: check vet test cover race diff soak
	@test -z "$$(gofmt -l .)" || (echo "not gofmt-clean:"; gofmt -l .; exit 1)
	@echo "all green"
