// Package fsx reads files the way a passive observer should. Opening a file
// must never get in the way of the program that owns it, a path that turns out
// not to be an ordinary file must not hang the reader, and a small metadata
// file must not be able to exhaust memory.
package fsx

import (
	"errors"
	"io"
	"os"
)

var (
	// ErrNotRegular is returned for pipes, devices, directories and the like.
	ErrNotRegular = errors.New("not a regular file")
	// ErrTooLarge is returned by ReadSmall when the file exceeds its limit.
	ErrTooLarge = errors.New("file larger than the limit")
)

// Open opens path for reading without restricting what other processes may do
// with it meanwhile; see the platform files for what that takes.
func Open(path string) (*os.File, error) { return open(path) }

// OpenRegular is Open plus a check that the handle refers to an ordinary
// file. The returned FileInfo comes from the open handle, so it describes
// exactly the file that will be read.
func OpenRegular(path string) (*os.File, os.FileInfo, error) {
	f, err := open(path)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	return f, st, nil
}

// ReadSmall reads a whole regular file of at most limit bytes.
func ReadSmall(path string, limit int64) ([]byte, error) {
	f, _, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &os.PathError{Op: "read", Path: path, Err: ErrTooLarge}
	}
	return data, nil
}
