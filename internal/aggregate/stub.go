//go:build !duckdb

package aggregate

import "errors"

// ErrNotBuilt is returned by the stub Run so the CLI can report a clean,
// actionable error rather than a "command not found". The real Run lives
// in ingest.go under `//go:build duckdb`.
var ErrNotBuilt = errors.New("aggregate subcommand requires a DuckDB build: rebuild with `go build -tags duckdb ./cmd/resolver` (see docs/build.md)")

// Run is a no-op stub. Build with `-tags duckdb` to get the aggregator.
func Run(opts Options) error { return ErrNotBuilt }
