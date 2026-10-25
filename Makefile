.PHONY: test test-py test-go build lint lint-scripts dashboard-install dashboard-build dashboard-test dashboard-embed install uninstall check

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
