package webui

import (
	"embed"
	"io/fs"
)

// frontendFS holds the built Vite output (HTML, JS, CSS, assets). The
// directory is committed as a placeholder (.gitkeep) for local Go builds; the
// Dockerfile rebuilds it before invoking go build so the production image
// always ships fresh assets.
//
//go:embed all:frontend/dist
var frontendFS embed.FS

// frontendDist returns the embedded frontend filesystem rooted at dist.
func frontendDist() fs.FS {
	sub, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		panic(err) // the embed directive guarantees the path exists
	}
	return sub
}
