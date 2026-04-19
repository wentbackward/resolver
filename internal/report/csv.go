package report

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gresham/resolver/internal/scenario"
)

// CSVWriter is a thin streaming wrapper around encoding/csv.
type CSVWriter struct {
	file *os.File
	w    *csv.Writer
}

// NewCSVWriter creates a file at {dir}/{modelSlug}_{sweep}_{iso}.csv and
// writes the header row.
func NewCSVWriter(dir, model, sweep string, ts time.Time, header []string) (*CSVWriter, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	name := fmt.Sprintf("%s_%s_%s.csv", scenario.ModelSlug(model), sweep, scenario.FilenameTimestamp(ts))
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return nil, "", err
	}
	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		f.Close()
		return nil, "", err
	}
	return &CSVWriter{file: f, w: w}, path, nil
}

func (c *CSVWriter) Write(row []string) error { return c.w.Write(row) }

func (c *CSVWriter) Close() error {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		_ = c.file.Close()
		return err
	}
	return c.file.Close()
}

// WriteAll is a convenience for small result sets (e.g. gate evaluators).
func WriteAll(out io.Writer, header []string, rows [][]string) error {
	w := csv.NewWriter(out)
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
