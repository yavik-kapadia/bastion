//go:build nofrontend

package main

import "io/fs"

// frontendFS returns nil in nofrontend builds (API-only mode, no embedded dashboard).
func frontendFS() fs.FS {
	return nil
}
