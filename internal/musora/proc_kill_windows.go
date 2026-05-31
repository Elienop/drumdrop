//go:build windows

package musora

import "os/exec"

// configureCancelKill is a no-op on Windows: there is no POSIX process group, so
// a context-cancel falls back to exec.CommandContext's default behavior (os.Kill
// of the yt-dlp process). The ffmpeg child it spawned may briefly linger, which
// is acceptable for the non-primary Windows build (the shipped runtime is the
// Linux container, where proc_kill_unix.go does the full process-group kill).
func configureCancelKill(cmd *exec.Cmd) {}
