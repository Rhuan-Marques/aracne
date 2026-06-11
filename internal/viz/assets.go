package viz

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var embeddedStatic embed.FS

func staticFileServer() http.Handler {
	files, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(files, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		if isAppRoute(r.URL.Path) {
			indexReq := *r
			indexReq.URL.Path = "/"
			fileServer.ServeHTTP(w, &indexReq)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func isAppRoute(path string) bool {
	return path == "/graph" || path == "/settings" || path == "/chat" || strings.HasPrefix(path, "/chat/")
}
