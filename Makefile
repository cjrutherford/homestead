.PHONY: test test-py test-go build lint lint-scripts dashboard-install dashboard-build install uninstall check

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
	bash -n scripts/install-systemd.sh
	bash -n scripts/uninstall-systemd.sh
	bash -n scripts/provision-layout.sh

install:
	sudo ./scripts/install-systemd.sh

uninstall:
	sudo ./scripts/uninstall-systemd.sh

dashboard-install:
	cd dashboard && pnpm install --frozen-lockfile

dashboard-build:
	cd dashboard && pnpm build

check: lint build test
