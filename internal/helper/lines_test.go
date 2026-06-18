package helper

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempLines(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func TestReadFileRange(t *testing.T) {
	p := writeTempLines(t, "l1\nl2\nl3\nl4\nl5\n")

	cases := []struct {
		name       string
		start, end int
		want       string
		wantLast   int
	}{
		{"full via zero", 0, 0, "l1\nl2\nl3\nl4\nl5", 5},
		{"middle", 2, 4, "l2\nl3\nl4", 4},
		{"single", 3, 3, "l3", 3},
		{"open end", 3, 0, "l3\nl4\nl5", 5},
		{"open start", 0, 2, "l1\nl2", 2},
		{"end past eof clamps", 4, 100, "l4\nl5", 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, last, err := ReadFileRange(p, c.start, c.end)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("content = %q, want %q", got, c.want)
			}
			if last != c.wantLast {
				t.Errorf("last = %d, want %d", last, c.wantLast)
			}
		})
	}
}

func TestReadFileRangeErrors(t *testing.T) {
	p := writeTempLines(t, "a\nb\nc\n")
	if _, _, err := ReadFileRange(p, 10, 20); err == nil {
		t.Error("expected error for start past EOF")
	}
	if _, _, err := ReadFileRange(p, 4, 2); err == nil {
		t.Error("expected error for end before start")
	}

	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(bin, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadFileRange(bin, 1, 0); err == nil {
		t.Error("expected error for binary file")
	}
}
