// Package history keeps what every session consumed, day by day, after the
// session has left the board, and after Claude Code has deleted its
// transcript. It holds counts only: a session's id, the folder it ran in, and
// its tokens per day and per model. Nothing that was said or run is kept, and
// no price: a cost is worked out from the counts when it is shown, so a
// corrected price table corrects the past as well.
//
// What is in the file cannot be read again from anywhere once the transcripts
// are gone, so the file is handled with care: it is replaced only by a whole
// new file that has reached the disk, a file that could not be understood is
// put aside rather than written over, and a file that could not be read at all
// is left exactly as it is.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"monitor-agent/internal/usage"
)

const (
	// maxFileBytes guards against a file that is not this program's. A year
	// of heavy use is a few megabytes.
	maxFileBytes = 64 << 20
	fileVersion  = 1
	maxIDBytes   = 128
	maxCwdBytes  = 4096
	// maxCount bounds one count of tokens. It is far above anything real and
	// far below where sums of such counts could wrap around.
	maxCount = 1_000_000_000_000_000
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
	live map[string]bool
	// gen goes up with every change; saved is the gen that is on disk.
	gen, saved int64
	// damaged is set when the file on disk was read but could not be
	// understood. It is put aside, not written over, at the next save.
	damaged bool
	// frozen is set when the file on disk could not be read at all. It may
	// be whole, only out of reach for a moment, and what it holds cannot be
	// had again from anywhere: nothing is saved over it in this run.
	frozen bool

	// saving is held while the file is being written, so that two saves
	// cannot interleave. It is taken before mu, never while holding it.
	saving sync.Mutex
}

// Open loads the ledger stored at path. A missing file is an empty ledger. A
// file that cannot be used gives an empty ledger and the error, so the app can
// start and say what is wrong: see Damaged and Frozen for what happens to the
// file. With an empty path nothing is ever saved.
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
		l.frozen = true
		return l, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxFileBytes {
		l.frozen = true
		return l, fmt.Errorf("%s is not a history file", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		// A sharing violation while a scanner or a backup has the file, a
		// network share that blinked: the file itself may be fine.
		l.frozen = true
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
		l.frozen = true
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
	in := func(n int64) int64 { return min(max(n, 0), maxCount) }
	return usage.Tokens{
		Input: in(tk.Input), Output: in(tk.Output), CacheRead: in(tk.CacheRead),
		CacheWrite5m: in(tk.CacheWrite5m), CacheWrite1h: in(tk.CacheWrite1h),
		WebSearches: in(tk.WebSearches), Requests: in(tk.Requests),
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
			merged[n-1].Tokens = clamp(merged[n-1].Tokens)
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// set stores an entry and notes that the ledger changed. The caller holds mu.
func (l *Ledger) set(id string, e Entry) {
	l.sessions[id] = e
	l.gen++
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
	l.set(id, Entry{Cwd: cwd, Rows: rows})
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
// An empty sig says the reading may be incomplete: it is kept, and the session
// is read again on the next pass.
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
	if len(rows) == 0 && sig == "" {
		// Nothing was read, and nothing says the files were read whole.
		return true
	}
	l.set(id, Entry{Cwd: cwd, Sig: sig, Rows: rows})
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

// Prune drops everything from before day. A session left with nothing goes,
// unless its files are known: then it stays, empty, so that it is not read
// again; see ForgetEmpty for when such a one goes too.
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
		if len(keep) == 0 && e.Sig == "" {
			delete(l.sessions, id)
			l.gen++
			continue
		}
		e.Rows = keep
		l.set(id, e)
	}
}

// ForgetEmpty drops the sessions that hold no rows and whose transcripts are
// gone: stillThere reports whether a session's files are on disk. Such an
// entry was kept only to save reading those files again, and with the files
// gone it would stay for ever.
func (l *Ledger) ForgetEmpty(stillThere func(id string) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, e := range l.sessions {
		if len(e.Rows) == 0 && !l.live[id] && !stillThere(id) {
			delete(l.sessions, id)
			l.gen++
		}
	}
}

// Damaged reports whether the file on disk was read and could not be
// understood. It is put aside under another name at the next save.
func (l *Ledger) Damaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.damaged
}

// Frozen reports whether the file on disk could not be read when the program
// started, or was written by a newer build. Nothing is saved in this run: the
// file is left as it is, and what this run learns is kept in memory only.
func (l *Ledger) Frozen() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.frozen
}

// aside is a name beside path that no file has yet, for putting a damaged
// file away without writing over one put away before.
func aside(path string) string {
	name := path + ".bak"
	if _, err := os.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return name
	}
	return path + "." + strconv.FormatInt(time.Now().Unix(), 10) + ".bak"
}

// replace moves from onto to. On Windows that fails while another program
// has the target open, a scanner or an indexer for an instant, so it is tried
// a few times.
func replace(from, to string) (err error) {
	for try := 0; try < 4; try++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(60 * time.Millisecond)
	}
	return err
}

// Save writes the ledger if it changed since it was last saved. The new file
// is written whole under another name, flushed to the disk, and only then
// moved into place: a crash or a power cut leaves either the old file or the
// new one, never half of one. Writing is done outside the lock that guards
// the ledger, so recording goes on meanwhile.
func (l *Ledger) Save() error {
	l.saving.Lock()
	defer l.saving.Unlock()

	l.mu.Lock()
	if l.path == "" || l.frozen || l.gen == l.saved {
		l.mu.Unlock()
		return nil
	}
	raw, err := json.Marshal(file{Version: fileVersion, Sessions: l.sessions})
	gen, damaged := l.gen, l.damaged
	l.mu.Unlock()
	if err != nil {
		return err
	}

	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A name of its own: two copies of the app must not write one file.
	tmp, err := os.CreateTemp(dir, filepath.Base(l.path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(append(raw, '\n'))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(name)
		return err
	}
	if damaged {
		// The file that could not be understood may still be worth a look,
		// or a repair by hand. It is put aside rather than written over.
		if err := replace(l.path, aside(l.path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(name)
			return fmt.Errorf("could not put the damaged history aside: %w", err)
		}
	}
	if err := replace(name, l.path); err != nil {
		_ = os.Remove(name)
		return err
	}

	l.mu.Lock()
	l.saved, l.damaged = gen, false
	l.mu.Unlock()
	return nil
}
