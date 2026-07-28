# libvips needs one preprocessor flag through cgo on macOS.
export CGO_CFLAGS_ALLOW = -Xpreprocessor

.PHONY: all check test race diff cover fmt vet

all: check test

check:
	@pkg-config --exists vips || \
		(echo "libvips not found: brew install vips, or apt install libvips-dev" && exit 1)
	@echo "libvips $$(pkg-config --modversion vips)"

test:
	go test ./vips/ ./internal/coverage/

race:
	go test -race ./vips/

# The oracle. Set VIPSX_IMAGE_DIR to add your own images to the comparison.
diff:
	go test ./internal/difftest/ -timeout 25m

cover:
	go test ./internal/coverage/ -v

fmt:
	gofmt -w .

vet:
	go vet ./...
