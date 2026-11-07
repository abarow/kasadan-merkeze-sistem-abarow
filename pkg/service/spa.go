// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"net/http"
	"os"
	"path/filepath"
)

// SPA serves the screen files (dir); unknown paths fall back to index.html
// (the single-page screen resolves its own paths in the browser). If the screen has not been
// built it says to build it in ui/apps/<app>.
func SPA(dir, app string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(filepath.Join(dir, filepath.Clean("/"+r.URL.Path))); err != nil {
			index := filepath.Join(dir, "index.html")
			if _, err := os.Stat(index); err != nil {
				http.Error(w, "Ekran derlenmemiş: ui/apps/"+app+" içinde npm run build çalıştırın.", http.StatusNotFound)
				return
			}
			http.ServeFile(w, r, index)
			return
		}
		files.ServeHTTP(w, r)
	})
}
