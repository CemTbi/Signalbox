// SPDX-License-Identifier: Apache-2.0

// Package webui serves the web app compiled into the server binary.
//
// "npm run build" in web/ writes the app into dist/, and go:embed bundles it.
// A binary built without the web app still runs and explains how to add it.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves the embedded web app.
func Handler() http.Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees dist exists
	}
	return NewHandler(dist)
}

// NewHandler serves a single-page app from files. Paths without a file
// extension get index.html, so client-side routes survive a reload. Missing
// files with an extension get a 404 rather than HTML the browser can't use.
func NewHandler(files fs.FS) http.Handler {
	index, indexErr := fs.ReadFile(files, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if indexErr != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(notBuiltPage)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		base := path.Base(name)
		if name != "" && !strings.HasPrefix(base, ".") {
			if data, err := fs.ReadFile(files, name); err == nil {
				if strings.HasPrefix(name, "assets/") {
					// Vite puts a content hash in every asset file name.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
				return
			}
			if strings.Contains(base, ".") {
				http.NotFound(w, r)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}

var notBuiltPage = []byte(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Signalbox</title>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 4rem auto; padding: 0 1rem; line-height: 1.5">
<h1>Signalbox is running</h1>
<p>This server was built without the web app, so only the API is available.</p>
<p>To include the web app, run <code>make web</code> and then <code>make build</code>, or use a release build.</p>
<p>Health: <a href="/readyz">/readyz</a> · API: <a href="/api/v1/meta">/api/v1/meta</a></p>
</body>
</html>
`)
