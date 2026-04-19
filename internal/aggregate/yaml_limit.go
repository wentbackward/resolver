package aggregate

import (
	"fmt"
	"io"
	"os"
)

// maxYAMLBytes is the per-file size cap for YAML inputs (1 MB).
// Prevents a malformed or adversarial file from exhausting memory during
// yaml.Unmarshal.
const maxYAMLBytes = 1 << 20 // 1 MB

// readCapped reads path and returns its contents, returning an error if the
// file exceeds limit bytes. The error message includes the file path so
// operators can identify which file triggered the cap.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lr := &io.LimitedReader{R: f, N: limit + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("%s: yaml exceeds %d bytes", path, limit)
	}
	return buf, nil
}
