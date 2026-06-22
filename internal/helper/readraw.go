package helper

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// ReadRawFile reads the file at path as text and returns "<basename>\n<content>".
// It rejects files larger than maxSize (a maxSize <= 0 disables the size check)
// and files that look binary (a NUL byte in the first 512 bytes). The not-found
// error is phrased for the topology read tools, which fall back here when an id
// is not a known resource.
func ReadRawFile(path string, maxSize int64) (string, error) {
	if info, err := os.Stat(path); err == nil && maxSize > 0 && info.Size() > maxSize {
		return "", fmt.Errorf("file %q is %d bytes, exceeding the configured read.max_file_size of %d bytes", path, info.Size(), maxSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file %q not found in topology and cannot be read as file: %w", path, err)
	}
	if isBinaryContent(data) {
		return "", fmt.Errorf("binary file %q cannot be displayed as text", path)
	}
	return fmt.Sprintf("%s\n%s", filepath.Base(path), string(data)), nil
}

// isBinaryContent reports whether data looks binary by scanning the first 512
// bytes for a NUL byte.
func isBinaryContent(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
