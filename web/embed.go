// Package web embeds the built single-page app. Run the frontend build
// (bun run build in web/) before compiling Go: a missing dist/ fails the Go
// build instead of shipping a placeholder or an empty shell.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets is the SPA file tree rooted at dist/ (index.html, assets/*).
var Assets = sub()

// sub cannot fail here: dist/ exists whenever the //go:embed above compiled,
// so an error means the build output itself is wrong.
func sub() fs.FS {
	tree, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("web: embedded dist is unusable: " + err.Error())
	}
	return tree
}
