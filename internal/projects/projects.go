// Package projects keeps the list of folders the user added to the app by
// hand. Those projects are shown even when no Claude Code session is running
// in them, and are where the app may start sessions of its own.
package projects

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"monitor-agent/internal/fsx"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// The list is small; the limit only guards against a file that is not ours.
const maxFileBytes = 1 << 20

type file struct {
	Projects []string `json:"projects"`
}

// Store is the saved list. It is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	dirs []string
	// damaged is set when the file on disk could not be used. That file is
	// put aside, not written over, the first time the list is saved again.
	damaged bool
}

// Key is the form in which two spellings of the same folder compare equal.
func Key(dir string) string {
	k := filepath.Clean(dir)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		k = strings.ToLower(k)
	}
	return k
}

// Open loads the list stored at path. A missing file is an empty list. A file
// that cannot be read gives an empty list and the error, so the app can start
// and say what is wrong.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		s.damaged = true
		return s, err
	}
	if len(raw) > maxFileBytes {
		s.damaged = true
		return s, fmt.Errorf("%s is too large to be a project list", path)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		s.damaged = true
		return s, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, d := range f.Projects {
		if d = strings.TrimSpace(d); d == "" || !filepath.IsAbs(d) || seen[Key(d)] {
			continue
		}
		seen[Key(d)] = true
		s.dirs = append(s.dirs, filepath.Clean(d))
	}
	return s, nil
}

// List returns the saved folders in the order they were added.
func (s *Store) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.dirs...)
}

// Has reports whether dir is in the list.
func (s *Store) Has(dir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index(dir) >= 0
}

func (s *Store) index(dir string) int {
	k := Key(dir)
	for i, d := range s.dirs {
		if Key(d) == k {
			return i
		}
	}
	return -1
}

// Damaged reports whether the list on disk could not be read and has not
// been replaced by a good one since.
func (s *Store) Damaged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.damaged
}

// Add saves dir, which must be an existing folder on a local drive, given as
// an absolute path. Adding a folder that is already listed changes nothing.
func (s *Store) Add(dir string) error {
	dir = strings.TrimSpace(dir)
	if err := fsx.LocalDir(dir); err != nil {
		return err
	}
	dir = filepath.Clean(dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index(dir) >= 0 {
		return nil
	}
	next := append(append([]string(nil), s.dirs...), dir)
	if err := s.save(next); err != nil {
		return err
	}
	s.dirs = next
	return nil
}

// Remove takes dir off the list. It does nothing to the folder itself.
func (s *Store) Remove(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(dir)
	if i < 0 {
		return nil
	}
	next := append(append([]string(nil), s.dirs[:i]...), s.dirs[i+1:]...)
	if err := s.save(next); err != nil {
		return err
	}
	s.dirs = next
	return nil
}

// save writes the list to a temporary file and moves it into place, so a
// crash mid-write cannot leave half a list behind.
func (s *Store) save(dirs []string) error {
	if s.path == "" {
		return errors.New("không xác định được nơi lưu danh sách project")
	}
	if dirs == nil {
		dirs = []string{}
	}
	raw, err := json.MarshalIndent(file{Projects: dirs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if s.damaged {
		// The file that could not be read is the only copy of a list someone
		// made. It is put aside rather than written over.
		if err := os.Rename(s.path, s.path+".bak"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(tmp)
			return fmt.Errorf("không cất được danh sách cũ bị lỗi: %w", err)
		}
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.damaged = false
	return nil
}
