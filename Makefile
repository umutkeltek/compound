.PHONY: install release-artifacts

VERSION ?= 0.1.0
DIST := dist
GO ?= go

PLATFORMS := darwin/arm64 linux/amd64

install:
	./install.sh

release-artifacts:
	@mkdir -p $(DIST)
	@build_sha=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown); \
	for pair in $(PLATFORMS); do \
		os=$${pair%/*}; \
		arch=$${pair#*/}; \
		stage=$(DIST)/compound_$(VERSION)_$${os}_$${arch}; \
		mkdir -p $$stage; \
		GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "-X main.buildSHA=$$build_sha" -o $$stage/compound ./cmd/compound; \
		GOOS=$$os GOARCH=$$arch $(GO) build -o $$stage/compound-breadth-engine ./cmd/compound-breadth-engine; \
		tar -C $(DIST) -czf $(DIST)/compound_$(VERSION)_$${os}_$${arch}.tar.gz compound_$(VERSION)_$${os}_$${arch}; \
		rm -rf $$stage; \
	done
	( cd $(DIST) && shasum -a 256 compound_$(VERSION)_*.tar.gz > SHA256SUMS )
	@echo "release-artifacts: wrote $(DIST)/compound_$(VERSION)_*.tar.gz + SHA256SUMS"
