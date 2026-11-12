// Package history keeps what every session consumed, day by day, after the
// session has left the board, and after Claude Code has deleted its
// transcript. It holds counts only: a session's id, the folder it ran in, and
// its tokens per day and per model. Nothing that was said or run is kept, and
// no price: a cost is worked out from the counts when it is shown, so a
// corrected price table corrects the past as well.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"monitor-agent/internal/usage"
)

const (
	// maxFileBytes guards against a file that is not this program's. A year
	// of heavy use is a few megabytes.
	maxFileBytes = 64 << 20
	fileVersion  = 1
	maxIDBytes   = 128
	maxCwdBytes  = 4096
)

// Row is what one model consumed for one session on one day.
type Row struct {
	// Day is a local calendar date, 2006-01-02.
	Day    string       `json:"day"`
	Model  string       `json:"model"`
	Fast   bool         `json:"fast,omitempty"`
	Tokens usage.Tokens `json:"tokens"`
}

// Entry is one session.
type Entry struct {
	// Cwd is the folder the session ran in, as it recorded it; empty when
	// its transcript did not say.
	Cwd string `json:"cwd"`
	// Sig describes the transcript files the rows were read from. A session
	// whose files still match it does not need reading again. It is empty
	// for an entry taken from the board, which reads its sessions live.
	Sig  string `json:"sig,omitempty"`
	Rows []Row  `json:"rows"`
}

type file struct {
	Version  int              `json:"version"`
	Sessions map[string]Entry `json:"sessions"`
}

// Ledger is the saved history. It is safe for concurrent use.
type Ledger struct {
	mu       sync.Mutex
	path     string
	sessions map[string]Entry
	// live holds the sessions the board has recorded since the program
	// started. The board reads those as they grow, so what it says about
	// them is newer than anything read from disk in one pass.
	live  map[string]bool
	dirty bool
	// damaged is set when the file on disk could not be used. It is put
	// aside, not written over, the first time the ledger is saved.
	damaged bool
}

// Open loads the ledger stored at path. A missing file is an empty ledger. A
// file that cannot be used gives an empty ledger and the error, so the app can
// start and say what is wrong. With an empty path nothing is ever saved.
func Open(path string) (*Ledger, error) {
	l := &Ledger{path: path, sessions: map[string]Entry{}, live: map[string]bool{}}
	if path == "" {
		return l, nil
	}
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		l.damaged = true
		return l, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxFileBytes {
		l.damaged = true
		return l, fmt.Errorf("%s is not a history file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		l.damaged = true
		return l, err
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		l.damaged = true
		return l, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version > fileVersion {
		// Written by a newer build. Reading it as this version could drop
		// what that build added, and saving would then lose it for good.
		l.damaged = true
		return l, fmt.Errorf("%s was written by a newer version of the app", path)
	}
	for id, e := range f.Sessions {
		if !validID(id) || len(e.Cwd) > maxCwdBytes {
			continue
		}
		e.Rows = tidy(e.Rows)
		if len(e.Rows) == 0 && e.Sig == "" {
			// Says nothing, and not even which files it says it about.
			continue
		}
		l.sessions[id] = e
	}
	return l, nil
}

func validID(id string) bool {
	return id != "" && len(id) <= maxIDBytes && !strings.ContainsAny(id, "\x00\r\n")
}

// ValidDay reports whether s has the shape of a calendar date, 2006-01-02.
// Dates in that shape compare as text the way they compare as dates.
func ValidDay(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 4 || i == 7 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func clamp(tk usage.Tokens) usage.Tokens {
	nn := func(n int64) int64 { return max(n, 0) }
	return usage.Tokens{
		Input: nn(tk.Input), Output: nn(tk.Output), CacheRead: nn(tk.CacheRead),
		CacheWrite5m: nn(tk.CacheWrite5m), CacheWrite1h: nn(tk.CacheWrite1h),
		WebSearches: nn(tk.WebSearches), Requests: nn(tk.Requests),
	}
}

// tidy returns rows in a fixed order with what cannot be used left out: a row
// without a proper day, without a model, or that counts nothing. Rows that
// share day, model and speed are merged. The result is always a new slice.
func tidy(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		r.Tokens = clamp(r.Tokens)
		if !ValidDay(r.Day) || r.Model == "" || len(r.Model) > maxIDBytes || r.Tokens == (usage.Tokens{}) {
			continue
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Row) int {
		if c := strings.Compare(a.Day, b.Day); c != 0 {
			return c
		}
		if c := strings.Compare(a.Model, b.Model); c != 0 {
			return c
		}
		switch {
		case a.Fast == b.Fast:
			return 0
		case a.Fast:
			return 1
		}
		return -1
	})
	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && merged[n-1].Day == r.Day && merged[n-1].Model == r.Model && merged[n-1].Fast == r.Fast {
			merged[n-1].Tokens.Add(r.Tokens)
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// Record takes what the board knows about a session it is showing. From then
// on the session counts as live: Fill leaves it alone until the program is
// started again.
func (l *Ledger) Record(id, cwd string, rows []Row) {
	if !validID(id) || len(cwd) > maxCwdBytes {
		return
	}
	rows = tidy(rows)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.live[id] = true
	old, had := l.sessions[id]
	if len(rows) == 0 {
		// Nothing with a date yet. Whatever was known stays.
		return
	}
	if had && old.Cwd == cwd && old.Sig == "" && slices.Equal(old.Rows, rows) {
		return
	}
	l.sessions[id] = Entry{Cwd: cwd, Rows: rows}
	l.dirty = true
}

// Known reports whether a session needs no reading from disk: the board has
// it, or its files are the ones its rows were read from.
func (l *Ledger) Known(id, sig string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live[id] {
		return true
	}
	e, ok := l.sessions[id]
	return ok && sig != "" && e.Sig == sig
}

// Fill takes what was read from a session's transcripts in one pass over the
// disk. It reports false, and changes nothing, for a session the board has:
// the board's reading is the newer one. A session read and found empty is
// remembered as such, so it is not read again while its files stay the same.
func (l *Ledger) Fill(id, cwd, sig string, rows []Row) bool {
	if !validID(id) || len(cwd) > maxCwdBytes {
		return false
	}
	rows = tidy(rows)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live[id] {
		return false
	}
	old, had := l.sessions[id]
	if had && old.Cwd == cwd && old.Sig == sig && slices.Equal(old.Rows, rows) {
		return true
	}
	l.sessions[id] = Entry{Cwd: cwd, Sig: sig, Rows: rows}
	l.dirty = true
	return true
}

// Each calls yield for every session. Rows must not be changed.
func (l *Ledger) Each(yield func(id string, e Entry)) {
	l.mu.Lock()
	snapshot := make(map[string]Entry, len(l.sessions))
	for id, e := range l.sessions {
		// Rows are replaced whole and never written in place, so the slice
		// can be read without the lock.
		snapshot[id] = e
	}
	l.mu.Unlock()
	for id, e := range snapshot {
		yield(id, e)
	}
}

// Prune drops everything from before day. Sessions left with nothing stay as
// empty entries when their files are known, so they are not read again.
func (l *Ledger) Prune(before string) {
	if !ValidDay(before) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, e := range l.sessions {
		keep := make([]Row, 0, len(e.Rows))
		for _, r := range e.Rows {
			if r.Day >= before {
				keep = append(keep, r)
			}
		}
		if len(keep) == len(e.Rows) {
			continue
		}
		l.dirty = true
		if len(keep) == 0 && e.Sig == "" {
			delete(l.sessions, id)
			continue
		}
		e.Rows = keep
		l.sessions[id] = e
	}
}

// Damaged reports whether the file on disk could not be used and has not been
// replaced by a good one since.
func (l *Ledger) Damaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.damaged
}

// Save writes the ledger if it changed since it was last saved. It writes to
// a temporary file and moves it into place, so a crash mid-write cannot leave
// half a ledger behind.
func (l *Ledger) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" || !l.dirty {
		return nil
	}
	raw, err := json.Marshal(file{Version: fileVersion, Sessions: l.sessions})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if l.damaged {
		// The file that could not be read may be the only copy of months of
		// history. It is put aside rather than written over.
		if err := os.Rename(l.path, l.path+".bak"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(tmp)
			return fmt.Errorf("could not put the damaged history aside: %w", err)
		}
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	l.dirty, l.damaged = false, false
	return nil
}
