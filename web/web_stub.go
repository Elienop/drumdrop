//go:build !webui

// Package web embeds the built single-page UI. Under the default build the UI is
// NOT embedded, so DistFS returns an empty file system; the server detects the
// missing index.html and serves a "web UI not built" notice. Build with
// `-tags webui` (after `make web`) to embed the real UI from web/dist.
package web

import "io/fs"

// emptyFS is an fs.FS with no files: every Open reports fs.ErrNotExist.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// DistFS returns an empty FS in the default build (no embedded UI).
func DistFS() fs.FS { return emptyFS{} }
