package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:webdist
var embeddedWeb embed.FS

func (srv *Server) mountStatic() {
	staticFS, err := fs.Sub(embeddedWeb, "webdist")
	if err != nil {
		panic(err)
	}

	srv.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		serveStaticShell(w, r, staticFS)
	})
}

func serveStaticShell(w http.ResponseWriter, r *http.Request, staticFS fs.FS) {
	if r.URL.Path == "/api" || r.URL.Path == "/ws" ||
		strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
		http.NotFound(w, r)
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}

	file, err := fs.Stat(staticFS, name)
	if err == nil && !file.IsDir() {
		http.ServeFileFS(w, r, staticFS, name)
		return
	}

	if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}

	http.ServeFileFS(w, r, staticFS, "index.html")
}
