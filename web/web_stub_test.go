//go:build !webui

package web

import (
	"errors"
	"io/fs"
	"testing"
)

func TestDistFSEmptyWithoutWebuiTag(t *testing.T) {
	_, err := DistFS().Open("index.html")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stub DistFS should report ErrNotExist for index.html, got %v", err)
	}
}
