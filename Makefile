.PHONY: test build fmt

test:
	go test ./...

fmt:
	go fmt ./...

build:
	mkdir -p bin
	go build -o bin/sqlserver2tidb ./cmd/sqlserver2tidb
	go build -o bin/sqlserver2tidb-executor ./cmd/sqlserver2tidb-executor
