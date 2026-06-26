VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
BUILDINFO_PACKAGE := github.com/BornChanger/sqlserver2tidb/internal/buildinfo
LDFLAGS := -X $(BUILDINFO_PACKAGE).Version=$(VERSION) -X $(BUILDINFO_PACKAGE).Commit=$(COMMIT) -X $(BUILDINFO_PACKAGE).BuildDate=$(BUILD_DATE)

.PHONY: test vet check build install dist validate-repo ci fmt

test:
	go test -count=1 ./...

vet:
	go vet ./...

check:
	git diff --check

fmt:
	go fmt ./...

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/sqlserver2tidb ./cmd/sqlserver2tidb
	go build -ldflags "$(LDFLAGS)" -o bin/sqlserver2tidb-executor ./cmd/sqlserver2tidb-executor

install: build
	mkdir -p "$(BINDIR)"
	install -m 0755 bin/sqlserver2tidb "$(BINDIR)/sqlserver2tidb"
	install -m 0755 bin/sqlserver2tidb-executor "$(BINDIR)/sqlserver2tidb-executor"

dist:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" DIST_DIR="$(DIST_DIR)" bash scripts/build-release.sh

validate-repo: build
	bin/sqlserver2tidb validate-repo --root .

ci: test vet check build validate-repo
