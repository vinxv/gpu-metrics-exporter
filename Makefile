.PHONY: all build clean test vet validate package

APP     := gpu-metrics-exporter
DIST    := dist
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
ZIP     := $(APP)-$(VERSION).zip
CONFIGS := $(wildcard configs/*.yaml)

# Default target
all: test vet build

# Build for current platform
build: $(DIST)
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP) .

# Cross-compile static binaries for Linux (CGO disabled: no libc dependency,
# runs on glibc / musl (Alpine) / older glibc systems).
linux-amd64: $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-linux-amd64 .

linux-arm64: $(DIST)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-linux-arm64 .

# Build all Linux archs
linux-all: linux-amd64 linux-arm64

$(DIST):
	mkdir -p $(DIST)

# Clean build artifacts
clean:
	rm -rf $(DIST) $(APP)-*.zip

# Run tests
test:
	go test ./... -count=1 -timeout 30s

# Validate all configs (like nginx -t)
validate: build
	@for f in $(CONFIGS); do \
		$(DIST)/$(APP) validate -config $$f || exit 1; \
	done

# Package all arch binaries, configs, README, and service file into a zip
PKG_DIR := $(DIST)/pkg/$(APP)-$(VERSION)
package: linux-all
	rm -rf $(DIST)/pkg $(ZIP)
	mkdir -p $(PKG_DIR)/dist $(PKG_DIR)/configs
	cp $(DIST)/$(APP)-linux-amd64 $(DIST)/$(APP)-linux-arm64 $(PKG_DIR)/dist/
	cp $(CONFIGS) $(PKG_DIR)/configs/
	cp README.md gpu-metrics-exporter.service $(PKG_DIR)/
	cd $(PKG_DIR) && zip -r ../../../$(ZIP) .
	@echo "==> $(ZIP) ready"

# Run vet
vet:
	go vet ./...

# Tidy dependencies
tidy:
	go mod tidy
