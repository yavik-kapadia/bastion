//go:build !nofrontend

package main

import (
	"embed"
	"io/fs"
	"log/slog"
)

// frontend/ is populated by `make build` (copies frontend/build here).
//
//go:embed all:frontend
var embeddedFrontend embed.FS

// frontendFS returns the embedded frontend build directory.
func frontendFS() fs.FS {
	sub, err := fs.Sub(embeddedFrontend, "frontend")
	if err != nil {
		slog.Warn("frontend not embedded", "err", err)
		return nil
	}
	return sub
}
