package fsx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LocalDir(dir); err != nil {
		t.Fatalf("an ordinary folder was refused: %v", err)
	}
	refused := map[string]string{
		"empty":    "",
		"relative": "some/folder",
		"missing":  filepath.Join(dir, "absent"),
		"a file":   file,
	}
	if runtime.GOOS == "windows" {
		// None of these may be looked up: the test would hang or sign in to a
		// server if they were.
		refused["a network share"] = `\\192.0.2.1\share\repo`
		refused["a share written with forward slashes"] = `//192.0.2.1/share/repo`
		refused["a device path"] = `\\.\C:\Windows`
		refused["an extended path"] = `\\?\C:\Windows`
	}
	for name, target := range refused {
		if err := LocalDir(target); err == nil {
			t.Errorf("%s (%q) was accepted", name, target)
		}
	}
}

func TestReadSmall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, err := ReadSmall(path, 5); err != nil || string(got) != "12345" {
		t.Fatalf("at the limit: %q, %v", got, err)
	}
	if _, err := ReadSmall(path, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: err = %v, want ErrTooLarge", err)
	}
	if _, err := ReadSmall(filepath.Join(dir, "absent.json"), 10); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: err = %v, want fs.ErrNotExist", err)
	}
	if _, err := ReadSmall(dir, 10); err == nil {
		t.Fatal("reading a directory must fail")
	}
}

// While the monitor holds a file open, its owner must still be able to move
// it away and to delete it. (Replacing it by renaming another file over it is
// not covered: Windows refuses that for any open handle unless the renamer
// uses POSIX semantics, which is outside this package's control.)
func TestOpenDoesNotBlockMoveOrDelete(t *testing.T) {
	dir := t.TempDir()

	moved := filepath.Join(dir, "moved.json")
	if err := os.WriteFile(moved, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	heldMoved, _, err := OpenRegular(moved)
	if err != nil {
		t.Fatal(err)
	}
	defer heldMoved.Close()
	if err := os.Rename(moved, filepath.Join(dir, "elsewhere.json")); err != nil {
		t.Fatalf("moving a file with an open reader failed: %v", err)
	}

	deleted := filepath.Join(dir, "deleted.json")
	if err := os.WriteFile(deleted, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	heldDeleted, _, err := OpenRegular(deleted)
	if err != nil {
		t.Fatal(err)
	}
	defer heldDeleted.Close()
	if err := os.Remove(deleted); err != nil {
		t.Fatalf("deleting a file with an open reader failed: %v", err)
	}
}

func TestOpenLongPath(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for len(deep) < 300 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Skipf("this system cannot create a long path: %v", err)
	}
	path := filepath.Join(deep, "t.jsonl")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Skipf("this system cannot write under a long path: %v", err)
	}
	if got, err := ReadSmall(path, 10); err != nil || string(got) != "x" {
		t.Fatalf("long path: %q, %v", got, err)
	}
}
