package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var distFS embed.FS

// FS returns the embedded SPA filesystem rooted at the dist directory.
func FS() http.FileSystem {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}
