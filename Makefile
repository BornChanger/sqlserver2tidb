VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
BUILDINFO_PACKAGE := github.com/BornChanger/sqlserver2tidb/internal/buildinfo
LDFLAGS := -X $(BUILDINFO_PACKAGE).Version=$(VERSION) -X $(BUILDINFO_PACKAGE).Commit=$(COMMIT) -X $(BUILDINFO_PACKAGE).BuildDate=$(BUILD_DATE)

.PHONY: test vet check build install dist dist-check dockerfile-check workflow-check example-check validate-repo ci fmt fmt-check script-check smoke-check

test:
	go test -count=1 ./...

vet:
	go vet ./...

check:
	git diff --check

fmt:
	go fmt ./...

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || (echo "gofmt required for:"; gofmt -l cmd internal; exit 1)

script-check:
	bash -n scripts/*.sh

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/sqlserver2tidb ./cmd/sqlserver2tidb
	go build -ldflags "$(LDFLAGS)" -o bin/sqlserver2tidb-executor ./cmd/sqlserver2tidb-executor

smoke-check: build
	bin/sqlserver2tidb version
	bin/sqlserver2tidb-executor version

install: build
	mkdir -p "$(BINDIR)"
	install -m 0755 bin/sqlserver2tidb "$(BINDIR)/sqlserver2tidb"
	install -m 0755 bin/sqlserver2tidb-executor "$(BINDIR)/sqlserver2tidb-executor"

dist:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" DIST_DIR="$(DIST_DIR)" bash scripts/build-release.sh

dist-check:
	@set -e; \
	dist_dir="$$(mktemp -d)"; \
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" DIST_TARGETS="linux/amd64" DIST_DIR="$$dist_dir" bash scripts/build-release.sh; \
	archive_count="$$(find "$$dist_dir" -maxdepth 1 -name '*.tar.gz' -print | wc -l | tr -d ' ')"; \
	if [ "$$archive_count" != "1" ]; then echo "expected one release archive, found $$archive_count in $$dist_dir" >&2; exit 1; fi; \
	archive="$$dist_dir/sqlserver2tidb_$(VERSION)_linux_amd64.tar.gz"; \
	test -s "$$archive"; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/README.md" >/dev/null; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/docs/user-manual.md" >/dev/null; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/examples/quickstart/inventory.json" >/dev/null; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/examples/worker-agent/.env.example" >/dev/null; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/examples/worker-agent/docker-compose.yaml" >/dev/null; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/examples/worker-agent/systemd/sqlserver2tidb-worker-agent.service" >/dev/null; \
	tar -tzf "$$archive" "sqlserver2tidb_$(VERSION)_linux_amd64/scripts/run-quickstart-example.sh" >/dev/null; \
	test -s "$$dist_dir/checksums.txt"; \
	if grep -q "/" "$$dist_dir/checksums.txt"; then echo "checksums.txt must list archive basenames, not paths" >&2; exit 1; fi

dockerfile-check:
	test -s Dockerfile
	grep -q '^FROM golang:' Dockerfile
	grep -q '^USER sqlserver2tidb' Dockerfile

workflow-check:
	test -s .github/workflows/container.yml
	grep -q 'packages: write' .github/workflows/container.yml
	grep -q 'GITHUB_REPOSITORY,,' .github/workflows/container.yml

example-check:
	bash scripts/run-quickstart-example.sh

validate-repo: build
	bin/sqlserver2tidb validate-repo --root .

ci: fmt-check script-check test vet check build smoke-check dist-check dockerfile-check workflow-check example-check validate-repo
