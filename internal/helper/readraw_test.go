package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRawFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadRawFile(path, 0)
	if err != nil {
		t.Fatalf("ReadRawFile: %v", err)
	}
	if !strings.HasPrefix(out, "note.txt\n") || !strings.Contains(out, "hello world") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestReadRawFileOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRawFile(path, 4); err == nil {
		t.Fatal("expected oversize error")
	}
}

func TestReadRawFileBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte{'a', 0x00, 'b'}, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRawFile(path, 0); err == nil {
		t.Fatal("expected binary error")
	}
}

func TestReadRawFileMissing(t *testing.T) {
	if _, err := ReadRawFile(filepath.Join(t.TempDir(), "nope.txt"), 0); err == nil {
		t.Fatal("expected missing-file error")
	}
}
