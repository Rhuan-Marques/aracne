package viz

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var embeddedStatic embed.FS

func staticFileServer() http.Handler {
	files, err := fs.Sub(embeddedStatic, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(files))
}
