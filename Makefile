.PHONY: release test test-py test-go build lint lint-scripts dashboard-install dashboard-build dashboard-test dashboard-embed install uninstall check

test: test-py test-go

test-py:
	python3 -m unittest discover -s tests -v

test-go:
	go test ./...

build:
	go build ./...

lint:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...

lint-scripts:
	bash -n scripts/acceptance.sh
	bash -n scripts/bootstrap.sh
	bash -n scripts/install-systemd.sh
	bash -n scripts/recover-control-plane.sh
	bash -n scripts/uninstall-systemd.sh
	bash -n scripts/factory-reset.sh
	bash -n scripts/provision-layout.sh
	bash -n scripts/craft-manifest.sh
	bash -n scripts/sign-craft-manifest.sh
	bash -n scripts/craft-header-check.sh
	bash -n scripts/everything/gate/run_compose_check.sh

acceptance:
	sudo ./scripts/acceptance.sh

bootstrap:
	sudo ./scripts/bootstrap.sh

install:
	sudo ./scripts/install-systemd.sh

uninstall:
	sudo ./scripts/uninstall-systemd.sh

recover:
	sudo ./scripts/recover-control-plane.sh

dashboard-install:
	cd dashboard && pnpm install --frozen-lockfile

dashboard-build:
	cd dashboard && pnpm build

dashboard-test:
	cd dashboard && pnpm exec ng test --watch=false

dashboard-embed:
	test -d dashboard/dist/homestead-dashboard/browser || (echo "run make dashboard-build first" >&2; exit 1)
	rm -rf internal/dashboard/dist
	mkdir -p internal/dashboard/dist
	cp -r dashboard/dist/homestead-dashboard/browser/. internal/dashboard/dist/

check: lint build test

# Release build (S332). VERSION is the single version source. Raise
# RELEASE_COUNTER with every release (ADR-025 rollback protection).
# Signing is offline and manual: this target never touches a key.
VERSION := $(shell cat VERSION)
RELEASE_COUNTER ?= 1
RELEASE_DIR := dist/release
# Production release public key (S493): the .pub file from
# scripts/release/keygen.sh. Defaults to release/release.pub when that file
# exists; empty keeps the placeholder key.
RELEASE_PUBLIC_KEY_FILE ?= $(wildcard release/release.pub)
RELEASE_PUBLIC_KEY := $(if $(RELEASE_PUBLIC_KEY_FILE),$(shell tail -n 1 $(RELEASE_PUBLIC_KEY_FILE)))

release: dashboard-build dashboard-embed
	rm -rf $(RELEASE_DIR)
	mkdir -p $(RELEASE_DIR)
	for arch in amd64 arm64; do \
	  for bin in homestead-server homesteadctl; do \
	    CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -tags embeddash -trimpath \
	      -ldflags "-s -w -X homestead/internal/version.Version=$(VERSION)$(if $(RELEASE_PUBLIC_KEY), -X homestead/internal/release.ReleasePublicKey=$(RELEASE_PUBLIC_KEY))" \
	      -o $(RELEASE_DIR)/$$bin-linux-$$arch ./cmd/$$bin || exit 1; \
	  done; \
	done
	{ echo "version=$(VERSION)"; echo "counter=$(RELEASE_COUNTER)"; \
	  cd $(RELEASE_DIR) && sha256sum homestead-server-linux-* homesteadctl-linux-*; } > $(RELEASE_DIR)/SHA256SUMS.tmp
	mv $(RELEASE_DIR)/SHA256SUMS.tmp $(RELEASE_DIR)/SHA256SUMS

