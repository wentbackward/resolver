# ADR 0002 — DuckDB aggregator behind `-tags duckdb`

**Status:** Accepted
**Date:** 2026-04-19
**Supersedes:** none
**Plan:** `.omc/plans/resolver-v2-plan.md`, Phase 2
**Spec:** `.omc/specs/deep-interview-resolver-v2-comparison.md`

## Context

v1 of `resolver` shipped as a single, CGO-free, statically-linkable Go
binary. Cross-compiling to any (OS, arch) pair with the target Go toolchain
Just Works™ — which was an explicit design goal (spec:52 "copy `reports/`
and run" portability).

v2 adds a cross-run aggregator. The best Go ecosystem fit for the
analytical-SQL surface the Python notebooks + `analyze report` CLI want is
DuckDB, via `github.com/marcboeker/go-duckdb`. That package is CGO-only: it
bundles DuckDB's native library and needs a C toolchain at build time.

Dropping CGO onto the default build would silently regress v1 portability
for the ~99 % of operators who just run the benchmark and don't do
cross-model aggregation. Asking every operator to install a C toolchain
crossed the Architect's line in iteration 2 of the plan review.

## Decision

Guard the DuckDB aggregator behind a Go build tag (`duckdb`). The default
`go build ./...` produces a CGO-free `resolver` binary in which the
`aggregate` subcommand stubs out with a clean, actionable error:

```
error: aggregate subcommand requires a DuckDB build: rebuild with
       `go build -tags duckdb ./cmd/resolver` (see docs/build.md)
```

Operators who need the aggregator build with `-tags duckdb`. CI runs
**both** modes on every push so neither regresses.

### Package layout

- `internal/aggregate/options.go` — always-present `Options` struct.
- `internal/aggregate/stub.go` (`//go:build !duckdb`) — stub `Run`
  returning `ErrNotBuilt`.
- `internal/aggregate/ingest.go` / `schema.go` (`//go:build duckdb`) — the
  real implementation using `database/sql` + `github.com/marcboeker/go-duckdb`.
- `internal/aggregate/community.go` — YAML validator; present in both
  builds because it doesn't touch DuckDB.
- `internal/aggregate/ingest_test.go` / `community_test.go` — one tagged,
  one untagged, matching the files they test.

### Rejected alternatives

- **Make `aggregate` a separate binary (`resolver-aggregate`).** Doubles
  the distribution story for marginal win; the subcommand-with-stub pattern
  preserves "one binary name, one CLI".
- **Switch to Parquet (pure-Go) as the canonical store.** Evaluated in
  the v2 plan Option D. Parquet-Go libraries are less mature and don't
  give us an SQL-queryable single file. Revisit in v2.1 if CGO friction
  proves real in the field.
- **Vendor a pure-Go DuckDB port.** None exists with adequate coverage.

## Consequences

**Positive:**
- v1's portability promise survives: default `go build ./...` still
  produces a CGO-free binary with every v1 feature.
- Operators who don't do cross-run aggregation never touch CGO.
- The `Options` struct is shared across build modes, so `cmd/resolver`
  compiles against a single API surface regardless of tag.

**Negative:**
- Two build targets to maintain in CI (one linux job adds the duckdb
  matrix cell — small cost).
- DuckDB builds cannot cross-compile without a CGO cross-toolchain. For
  now, build on-target. Documented in `docs/build.md`.
- `go.mod` lists `go-duckdb` as a direct dependency regardless of tag
  (Go's module resolver doesn't honour build tags for dep graphs).
  The dep is downloaded but not linked in the default build.

## Verification

- `go build ./...` (no tag) produces a CGO-free binary; `file
  $(which resolver)` shows statically-linked ELF on Linux.
- `go build -tags duckdb ./cmd/resolver` links against the bundled
  DuckDB C library and produces a working aggregator binary.
- `make build-all` runs both in sequence; `make test-all` runs both
  test suites.
- `.github/workflows/go.yml` has two jobs: `test` (default) and
  `test-duckdb` (tagged). Both must pass for every push / PR.

## Follow-ups

- If the dual-build CI job matrix becomes painful, consider migrating
  the aggregator to a separate `cmd/resolver-aggregate` entrypoint so
  the default binary's module graph stays tag-clean.
- Re-evaluate Parquet-as-intermediate (ADR-0002-alt?) in v2.1 if
  operators report CGO friction that can't be resolved by shipping
  pre-built binaries via GitHub Releases + SLSA.
