//go:build !windows

package musora

import (
	"os/exec"
	"syscall"
)

// configureCancelKill makes a context-cancel of cmd SIGKILL the whole process
// group, not just the leader. yt-dlp spawns ffmpeg to merge the streams; a bare
// kill of yt-dlp would orphan ffmpeg and leave it finishing the merge. Setpgid
// puts yt-dlp in its own group and Cancel signals the negative pid (the group).
func configureCancelKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
