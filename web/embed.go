// Package web embeds the built control-room UI (vite output in dist/).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the UI file tree, or nil if only the placeholder exists
// (i.e. `vite build` has not run).
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	if f, err := sub.Open("index.html"); err != nil {
		return nil
	} else {
		f.Close()
	}
	return sub
}
