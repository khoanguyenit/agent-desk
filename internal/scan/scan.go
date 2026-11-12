// Package scan finds the Claude Code sessions that are running on this machine
// and the transcript files that belong to them. It only reads; it never writes
// to the Claude config directory and never opens credential files.
package scan

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"monitor-agent/internal/fsx"
)

// Registry entries and agent meta files are a few hundred bytes. The limit
// only stops a file that is not what it claims to be from being read whole.
const maxSmallFile = 1 << 20

// ConfigDir returns the Claude Code config directory: $CLAUDE_CONFIG_DIR when
// set, otherwise ~/.claude.
func ConfigDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// Session is one running Claude Code process, as it describes itself in the
// session registry.
type Session struct {
	PID        int
	SessionID  string
	Cwd        string
	Name       string
	Status     string
	Kind       string
	Entrypoint string
	Version    string
	// WaitingFor is what a session with the status "waiting" says it is
	// held up by, in Claude Code's own words ("input needed").
	WaitingFor string
	StartedAt  time.Time
	UpdatedAt  time.Time

	procStart string
}

// AliveFunc reports whether the process that wrote a registry entry is still
// running. procStart is the opaque start marker from the entry; it guards
// against the pid having been reused by an unrelated process.
type AliveFunc func(pid int, procStart string) bool

// ProcessAlive is the real AliveFunc for this operating system.
func ProcessAlive(pid int, procStart string) bool { return processAlive(pid, procStart) }

// flexString accepts a JSON string or number, because the start marker is
// written differently per platform.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexString(n.String())
		return nil
	}
	*f = ""
	return nil
}

type registryEntry struct {
	PID        int        `json:"pid"`
	SessionID  string     `json:"sessionId"`
	Cwd        string     `json:"cwd"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Kind       string     `json:"kind"`
	Entrypoint string     `json:"entrypoint"`
	Version    string     `json:"version"`
	WaitingFor string     `json:"waitingFor"`
	ProcStart  flexString `json:"procStart"`
	StartedAt  int64      `json:"startedAt"`
	UpdatedAt  int64      `json:"updatedAt"`
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func readEntry(path string) (Session, bool) {
	raw, err := fsx.ReadSmall(path, maxSmallFile)
	if err != nil {
		return Session{}, false
	}
	var r registryEntry
	if err := json.Unmarshal(raw, &r); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return Session{}, false
		}
	}
	if r.PID <= 0 || r.SessionID == "" || r.Cwd == "" {
		return Session{}, false
	}
	return Session{
		PID:        r.PID,
		SessionID:  r.SessionID,
		Cwd:        r.Cwd,
		Name:       r.Name,
		Status:     r.Status,
		Kind:       r.Kind,
		Entrypoint: r.Entrypoint,
		Version:    r.Version,
		WaitingFor: r.WaitingFor,
		StartedAt:  msToTime(r.StartedAt),
		UpdatedAt:  msToTime(r.UpdatedAt),
		procStart:  string(r.ProcStart),
	}, true
}

// Registry reads the session registry and remembers the last good reading of
// each entry. Claude Code rewrites an entry whenever a session's status
// changes; a read that lands in the middle of that sees an empty or partial
// file, and without the memory the session would vanish for one refresh.
type Registry struct {
	last map[string]Session
}

// Live returns the sessions under <configDir>/sessions whose process is still
// running, oldest first. Entries left behind by processes that have exited
// are skipped.
func (r *Registry) Live(configDir string, alive AliveFunc) ([]Session, error) {
	dir := filepath.Join(configDir, "sessions")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		r.last = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]Session, len(entries))
	var out []Session
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		s, ok := readEntry(filepath.Join(dir, name))
		if !ok {
			// Unreadable right now: fall back to what it said last time, as
			// long as the file is still there.
			if s, ok = r.last[name]; !ok {
				continue
			}
		}
		seen[name] = s
		if alive(s.PID, s.procStart) {
			out = append(out, s)
		}
	}
	r.last = seen
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].PID < out[j].PID
	})
	return out, nil
}

// LiveSessions is Registry.Live without memory between calls.
func LiveSessions(configDir string, alive AliveFunc) ([]Session, error) {
	return new(Registry).Live(configDir, alive)
}

// Slug is the directory name Claude Code derives from a working directory:
// every character that is not an ASCII letter or digit becomes a dash. Claude
// Code does this per UTF-16 code unit, so a character outside the Basic
// Multilingual Plane becomes two dashes.
func Slug(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// validID guards the session id before it is used in a file name.
func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// TranscriptPath locates the main transcript of a session. It tries the
// directory derived from the working directory first and falls back to
// searching every project directory for the session id.
func TranscriptPath(configDir string, s Session) (string, bool) {
	if !validID(s.SessionID) {
		return "", false
	}
	projects := filepath.Join(configDir, "projects")
	name := s.SessionID + ".jsonl"
	direct := filepath.Join(projects, Slug(s.Cwd), name)
	if isFile(direct) {
		return direct, true
	}
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return "", false
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		p := filepath.Join(projects, d.Name(), name)
		if isFile(p) {
			return p, true
		}
	}
	return "", false
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Transcript is a session's main transcript as found on disk.
type Transcript struct {
	SessionID string
	Path      string
	ModTime   time.Time
	Size      int64
}

// TranscriptsSince lists the main transcripts under configDir that were
// written to at or after since, whether or not their session is still
// running. It is how sessions that have ended are found: Claude Code keeps a
// session's transcript after its process is gone and its registry entry with
// it. Directories that cannot be read are skipped.
func TranscriptsSince(configDir string, since time.Time) []Transcript {
	projects := filepath.Join(configDir, "projects")
	dirs, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	var out []Transcript
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(projects, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			id := strings.TrimSuffix(name, ".jsonl")
			if id == name || !e.Type().IsRegular() || !validID(id) {
				continue
			}
			info, err := e.Info()
			if err != nil || info.ModTime().Before(since) {
				continue
			}
			out = append(out, Transcript{SessionID: id, Path: filepath.Join(dir, name), ModTime: info.ModTime(), Size: info.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// AgentFile is the transcript of one subagent.
type AgentFile struct {
	ID         string
	Path       string
	MetaPath   string
	WorkflowID string
}

// AgentFiles lists the subagent transcripts of the session whose main
// transcript is transcriptPath, including agents started by workflows, which
// live in nested directories. Directories that cannot be read are skipped.
func AgentFiles(transcriptPath string) []AgentFile {
	root := filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents")
	var out []AgentFile
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		af := AgentFile{
			ID:       strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl"),
			Path:     p,
			MetaPath: strings.TrimSuffix(p, ".jsonl") + ".meta.json",
		}
		if rel, err := filepath.Rel(root, filepath.Dir(p)); err == nil {
			for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
				if strings.HasPrefix(part, "wf_") {
					af.WorkflowID = part
				}
			}
		}
		out = append(out, af)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// AgentMeta is what Claude Code records about a subagent next to its
// transcript.
type AgentMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	Model         string `json:"model"`
	PickedModel   string `json:"pickedEffortModel"`
	WorkflowPhase string `json:"workflowPhase"`
	RequestShape  string `json:"requestShape"`
	SpawnDepth    int    `json:"spawnDepth"`
	Finished      bool   `json:"finished"`
}

// ReadMeta reads a subagent's meta file. A missing file yields an empty meta.
func ReadMeta(path string) (AgentMeta, error) {
	var m AgentMeta
	raw, err := fsx.ReadSmall(path, maxSmallFile)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return AgentMeta{}, err
		}
	}
	return m, nil
}
