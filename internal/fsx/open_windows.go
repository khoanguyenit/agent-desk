//go:build windows

package fsx

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// open differs from os.Open in one respect: it also grants FILE_SHARE_DELETE.
// Go's own open does not, and while such a handle exists the file's owner can
// neither delete it nor move it. With the flag both work.
//
// One case remains that no share mode covers: replacing the file by renaming
// another one over it fails while any handle is open, unless the renamer asks
// for POSIX semantics. The reads here last microseconds, so that window is
// small, but it is not zero.
func open(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// longPath adds the prefix that lifts the 260-character limit, which
// CreateFile enforces unless the path is given in this form.
func longPath(path string) string {
	if len(path) < 248 || strings.HasPrefix(path, `\\`) || !filepath.IsAbs(path) {
		return path
	}
	return `\\?\` + filepath.Clean(path)
}
