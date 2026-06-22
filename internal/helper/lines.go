package helper

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"
)

// ReadFileRange returns lines [start, end] (1-indexed, inclusive) of the file at
// path. A start <= 0 defaults to the first line; an end <= 0 reads through EOF.
// It returns the sliced content and the number of the last line emitted.
// It streams the file line by line, so it handles large files and one-sided
// ranges without loading the whole file into memory, and is not bounded by the
// configured read.max_file_size (the requested range itself bounds the output).
// It errors on binary files or when start is past the end of the file.
func ReadFileRange(path string, start, end int) (string, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	// Binary sniff on the first chunk: a NUL byte means the file is not text.
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return "", 0, fmt.Errorf("binary file %q cannot be displayed as text", path)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", 0, err
	}

	if start <= 0 {
		start = 1
	}
	if end > 0 && end < start {
		return "", 0, fmt.Errorf("end line %d is before start line %d", end, start)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var b strings.Builder
	line, last := 0, 0
	for scanner.Scan() {
		line++
		if line < start {
			continue
		}
		if end > 0 && line > end {
			break
		}
		if last != 0 {
			b.WriteByte('\n')
		}
		b.WriteString(scanner.Text())
		last = line
	}
	if err := scanner.Err(); err != nil {
		return "", 0, err
	}
	if last == 0 {
		return "", 0, fmt.Errorf("start line %d is past end of file (%d lines)", start, line)
	}
	return b.String(), last, nil
}
