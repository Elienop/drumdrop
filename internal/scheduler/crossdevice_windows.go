//go:build windows

package scheduler

import "golang.org/x/sys/windows"

// errCrossDevice is what a rename answers when the two folders are on
// different volumes, the one rename failure a move copies after. os.Rename
// calls MoveFileEx without MOVEFILE_COPY_ALLOWED, which answers
// ERROR_NOT_SAME_DEVICE (Windows has no EXDEV; Go's syscall.EXDEV there is an
// invented value no call returns).
var errCrossDevice error = windows.ERROR_NOT_SAME_DEVICE
