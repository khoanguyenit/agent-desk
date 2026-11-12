//go:build !windows

package fsx

import (
	"os"
	"syscall"
)

// open uses O_NONBLOCK so that a path which is really a FIFO returns at once
// instead of waiting for a writer; for an ordinary file the flag changes
// nothing. Unix has no sharing modes, so a reader never blocks a rename.
func open(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
