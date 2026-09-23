//go:build !windows

package scheduler

import "syscall"

// errCrossDevice is what a rename answers when the two folders are on
// different filesystems (EXDEV), the one rename failure a move copies after.
var errCrossDevice error = syscall.EXDEV
