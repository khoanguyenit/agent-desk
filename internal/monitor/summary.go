package monitor

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"monitor-agent/internal/parse"
	"monitor-agent/internal/scan"
)

// summaryNames bounds the lists a summary hands out: tools, files, who
// allowed what.
const summaryNames = 8

// Count is a name with how many times it came up.
type Count struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

// Summary is a session's whole work added up, for the side panel: what was
// asked, what changed, who allowed the tool calls, and what made it cost
// what it did. See parse.Summary for what each figure counts.
type Summary struct {
	SessionID string  `json:"sessionId"`
	Prompts   int     `json:"prompts"`
	ToolCalls int     `json:"toolCalls"`
	Tools     []Count `json:"tools"`
	// Agents counts the subagents read, Workflows the workflow runs among
	// them.
	Agents    int `json:"agents"`
	Workflows int `json:"workflows"`
	// Files are the files edited, relative to the session's folder when
	// inside it, most edited first. FilesChanged counts them all; Files is
	// empty when contents are hidden.
	FilesChanged int     `json:"filesChanged"`
	Files        []Count `json:"files"`
	LinesAdded   int64   `json:"linesAdded"`
	LinesRemoved int64   `json:"linesRemoved"`
	Commits      int     `json:"commits"`
	Pushes       int     `json:"pushes"`
	FirstAtMs    int64   `json:"firstAtMs"`
	LastAtMs     int64   `json:"lastAtMs"`
	ToolMs       int64   `json:"toolMs"`
	Allowed      []Count `json:"allowed"`
	Denied       []Count `json:"denied"`
	Modes        []Count `json:"modes"`
	Compactions  int     `json:"compactions"`
	// CompactedFrom and CompactedTo are the context's size around the last
	// compaction.
	CompactedFrom   int64   `json:"compactedFrom"`
	CompactedTo     int64   `json:"compactedTo"`
	CacheMisses     int     `json:"cacheMisses"`
	CacheMissTokens int64   `json:"cacheMissTokens"`
	Responses       int     `json:"responses"`
	OutputTokens    int64   `json:"outputTokens"`
	ThinkingTokens  int64   `json:"thinkingTokens"`
	Efforts         []Count `json:"efforts"`
	// Partial is true when a transcript could not be read whole; Hidden when
	// the file names were left out on request.
	Partial bool `json:"partial"`
	Hidden  bool `json:"hidden"`
}

// summaryCache is the summary last handed out, with the state of the files it
// was read from: the whole transcript is read again only when one changed.
type summaryCache struct {
	sessionID string
	sig       string
	hide      bool
	summary   Summary
}

// Summary adds up what a session did, from its main transcript and its
// subagents'. Like Trail, it reaches only sessions on the board: the running
// ones and the ones listed as closed today. It reads every file in full, so
// the page asks for it when it is shown, not on every refresh.
func (s *Service) Summary(sessionID string, hide bool) (Summary, error) {
	s.mu.Lock()
	main, cwd, agents, err := s.sessionFiles(sessionID)
	cached := s.summary
	s.mu.Unlock()
	if err != nil {
		return Summary{SessionID: sessionID}, err
	}
	paths := make([]string, 0, len(agents))
	workflows := map[string]bool{}
	for _, af := range agents {
		paths = append(paths, af.Path)
		if af.WorkflowID != "" {
			workflows[af.WorkflowID] = true
		}
	}
	sig := filesSig(append([]string{main}, paths...))
	if cached.sessionID == sessionID && cached.sig == sig && cached.hide == hide {
		return cached.summary, nil
	}

	sum, err := parse.Summarize(main, paths)
	if err != nil {
		return Summary{SessionID: sessionID}, errors.New("không đọc được transcript của session này")
	}
	out := Summary{
		SessionID: sessionID, Prompts: sum.Prompts, ToolCalls: sum.ToolCalls, Tools: counts(sum.Tools, summaryNames),
		Agents: len(agents), Workflows: len(workflows),
		FilesChanged: len(sum.Files), Files: []Count{},
		LinesAdded: sum.LinesAdded, LinesRemoved: sum.LinesRemoved, Commits: sum.Commits, Pushes: sum.Pushes,
		FirstAtMs: ms(sum.FirstAt), LastAtMs: ms(sum.LastAt), ToolMs: sum.ToolTime.Milliseconds(),
		Allowed: counts(sum.Allowed, summaryNames), Denied: counts(sum.Denied, summaryNames), Modes: counts(sum.Modes, summaryNames),
		Compactions: sum.Compactions, CompactedFrom: sum.CompactedFrom, CompactedTo: sum.CompactedTo,
		CacheMisses: sum.CacheMisses, CacheMissTokens: sum.CacheMissTokens,
		Responses: sum.Responses, OutputTokens: sum.OutputTokens, ThinkingTokens: sum.ThinkingTokens,
		Efforts: counts(sum.Efforts, summaryNames), Partial: sum.Partial, Hidden: hide,
	}
	if !hide {
		// Paths inside the session's folder are shown relative to it, and
		// edits to one file add up however its path was written.
		files := map[string]int{}
		for p, n := range sum.Files {
			files[relativeTo(cwd, p)] += n
		}
		out.Files = counts(files, summaryNames)
	}

	s.mu.Lock()
	s.summary = summaryCache{sessionID: sessionID, sig: sig, hide: hide, summary: out}
	s.mu.Unlock()
	return out, nil
}

// sessionFiles finds a session's transcripts. Only sessions on the board are
// looked at, the running ones and the ones listed as closed today, so an id
// can never lead outside what the board already shows. The caller holds s.mu.
func (s *Service) sessionFiles(sessionID string) (main, cwd string, agents []scan.AgentFile, err error) {
	if s.configErr != nil {
		return "", "", nil, s.configErr
	}
	sessions, err := s.registry.Live(s.configDir, s.alive)
	if err != nil {
		return "", "", nil, errors.New("không đọc được danh sách session")
	}
	for _, rs := range sessions {
		if rs.SessionID != sessionID {
			continue
		}
		path, ok := scan.TranscriptPath(s.configDir, rs)
		if !ok {
			return "", "", nil, errors.New("session này chưa có transcript")
		}
		return path, rs.Cwd, scan.AgentFiles(path), nil
	}
	if c, ok := s.closed[sessionID]; ok {
		return c.path, c.cwd, c.agents, nil
	}
	return "", "", nil, errors.New("session này không còn trên bảng")
}

// filesSig is the state of a set of files: their sizes and modification
// times. A file that cannot be looked at counts as changed every time.
func filesSig(paths []string) string {
	var size, latest int64
	missing := 0
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			missing++
			continue
		}
		size += st.Size()
		latest = max(latest, st.ModTime().UnixNano())
	}
	return fmt.Sprintf("%d:%d:%d:%d", len(paths), missing, size, latest)
}

// counts lists names by how often they came up, most first, at most limit of
// them.
func counts(m map[string]int, limit int) []Count {
	out := make([]Count, 0, len(m))
	for name, n := range m {
		out = append(out, Count{Name: name, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
