package helper

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Normalizes resource IDs by expanding file URIs and tilde paths.
func NormalizeResourceID(id string) string {
	if id == "" {
		return id
	}

	if strings.HasPrefix(id, "file://") {
		return normalizeFileURI(id)
	}

	if strings.HasPrefix(id, "~") {
		return expandTilde(id)
	}

	return id
}

// Converts a file:// URI to a platform-appropriate filesystem path, handling Windows drive letter prefixes.
func normalizeFileURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return raw
	}

	path := u.Path

	if runtime.GOOS == "windows" && len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}

	return path
}

// Expands ~ to the user's home directory in a file path.
func expandTilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if path == "~" {
		return home
	}

	if len(path) >= 2 {
		prefix := path[:2]
		if prefix == "~/" || prefix == "~\\" {
			return filepath.Join(home, path[2:])
		}
	}

	return path
}
