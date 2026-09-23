package server

import (
	"path/filepath"
	"testing"
)

// TestServerHostPath covers the container→host download-path rewrite that backs
// the UI's "Copy path": with a host downloads dir configured the container
// prefix is swapped, and without one (or for a path outside the root, or a nil
// pointer) the value is returned unchanged.
func TestServerHostPath(t *testing.T) {
	strp := func(s string) *string { return &s }
	deref := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}

	s := &Server{cfg: Config{DownloadsDir: "/downloads", HostDownloadsDir: "/mnt/host/dl"}}

	// Under the container root: prefix rewritten to the host dir, rest preserved.
	in := "/downloads/30-Day Independence/01 - Course Kick-Off/01 - Course Kick-Off.mp4"
	want := "/mnt/host/dl/30-Day Independence/01 - Course Kick-Off/01 - Course Kick-Off.mp4"
	if got := s.hostPath(strp(in)); deref(got) != want {
		t.Errorf("hostPath(%q) = %q, want %q", in, deref(got), want)
	}

	// nil pointer stays nil.
	if got := s.hostPath(nil); got != nil {
		t.Errorf("hostPath(nil) = %q, want <nil>", deref(got))
	}

	// No host dir configured: unchanged (default behavior, container path).
	noHost := &Server{cfg: Config{DownloadsDir: "/downloads"}}
	if got := noHost.hostPath(strp(in)); deref(got) != in {
		t.Errorf("hostPath without host dir = %q, want unchanged %q", deref(got), in)
	}

	// Path outside the container root: unchanged (no accidental rewrite/traversal).
	out := "/etc/passwd"
	if got := s.hostPath(strp(out)); deref(got) != out {
		t.Errorf("hostPath(outside root) = %q, want unchanged %q", deref(got), out)
	}
}

// TestServerHostPathReadsARelativeRow proves a row recorded before the roots
// were made absolute (under the relative ./downloads default) is still mapped:
// it is read against the working directory, as the OS reads it, and compared
// with the absolute downloads root the server now has.
func TestServerHostPathReadsARelativeRow(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	s := &Server{cfg: Config{DownloadsDir: filepath.Join(tmp, "downloads"), HostDownloadsDir: "/mnt/host/dl"}}
	in := filepath.Join("downloads", "Course", "01 - One")
	want := filepath.Join("/mnt/host/dl", "Course", "01 - One")
	if got := s.hostPath(&in); got == nil || *got != want {
		t.Errorf("hostPath(%q) = %v, want %q", in, got, want)
	}
}
