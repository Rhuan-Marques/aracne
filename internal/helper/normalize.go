package helper

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
