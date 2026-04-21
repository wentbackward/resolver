# Resolver build + test targets.
#
# Two binary surfaces:
#   - default (CGO-free): `go build ./...` → pure-Go `resolver` binary.
#     The `aggregate` subcommand stubs out with an actionable error.
#   - with -tags duckdb (CGO): `go build -tags duckdb ./...` → adds the
#     DuckDB aggregator. Requires a C toolchain + libc. See docs/archive/build.md.

.PHONY: build build-duckdb build-all test test-duckdb test-all vet fmt clean

build:
	go build ./...

build-duckdb:
	go build -tags duckdb ./...

build-all: build build-duckdb

test:
	go test -count=1 ./...

test-duckdb:
	go test -tags duckdb -count=1 ./...

test-all: test test-duckdb

vet:
	go vet ./...
	go vet -tags duckdb ./...

fmt:
	gofmt -w .

clean:
	rm -rf reports/resolver.duckdb reports/results reports/sweeps

# CI convenience: everything green.
ci: vet test-all
