# Building resolver

Two build modes, one binary name, one CLI.

## Default (pure-Go, no CGO)

```bash
go build ./cmd/resolver
# or
make build
```

This is the v1 distributable binary. Cross-compiles cleanly from any
Linux/macOS/Windows host. Every v1 feature is available (Tier 1, Tier 2
multi-turn, sweeps A + B, gate policies, replay, emit-replay, threshold
override, run-config sidecar, `/v1/models` probe). The **only** thing
missing is the `aggregate` subcommand, which stubs out with a clear
error:

```
$ resolver aggregate
error: aggregate subcommand requires a DuckDB build: rebuild with
       `go build -tags duckdb ./cmd/resolver` (see docs/build.md)
```

## With DuckDB (tagged, CGO)

```bash
go build -tags duckdb ./cmd/resolver
# or
make build-duckdb
```

Links against `github.com/marcboeker/go-duckdb`, which bundles the
DuckDB C library and requires a C toolchain + libc on the build host.

### Prerequisites

- Linux/macOS: `gcc` or `clang`, standard libc.
- Windows: MSYS2 / MinGW-w64 (`mingw-w64-x86_64-gcc`) or WSL.
- CGO enabled: `CGO_ENABLED=1` (default when a C toolchain is detected).

### Cross-compilation

CGO builds cannot cross-compile by default. To build a DuckDB binary for a
different OS/arch, either:

1. Run the build on the target platform directly (simplest).
2. Set up a CGO cross-toolchain (`CC`, `CXX`, `AR` env vars) matching
   the target. See [Go CGO cross-compile](https://github.com/karalabe/xgo)
   for one approach.

The default CI matrix builds both modes on Linux only. If you need a
DuckDB macOS/Windows binary, run `make build-duckdb` on that OS.

## Both binaries at once

```bash
make build-all
```

Produces two binaries in sequence:
- `resolver` (default, CGO-free)
- `resolver` **rebuilt** with `-tags duckdb` (overwrites the first)

Rename or `-o` as you see fit if you need both side by side:

```bash
go build -o resolver-minimal ./cmd/resolver
go build -tags duckdb -o resolver-full ./cmd/resolver
```

## Running tests

```bash
make test-all       # default + duckdb tags
make test           # default only
make test-duckdb    # duckdb-tagged only
```

The default suite covers everything except `internal/aggregate/`. The
tagged suite adds the aggregator tests.

## Why the build-tag split?

See [`docs/adr/0002-duckdb-behind-build-tag.md`](./adr/0002-duckdb-behind-build-tag.md).
Short version: v1 shipped a static Go binary that cross-compiles freely.
v2's DuckDB aggregator wants CGO. Hiding CGO behind a build tag
preserves the v1 portability promise for the 99 % of operators who run
the benchmark but don't do cross-model aggregation, while the ~1 %
who do opt in with one extra flag.
