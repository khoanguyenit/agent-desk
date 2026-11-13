package monitor

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"monitor-agent/internal/parse"
	"monitor-agent/internal/scan"
)

const (
	// trailBytes is how much of the end of a transcript is read for a trail,
	// and trailSteps how many steps of it are handed to the page.
	trailBytes = 1 << 20
	trailSteps = 120
)

// TrailStep is one thing an agent did, as the detail panel shows it.
type TrailStep struct {
	// Kind is "prompt", "text", "tool" or "report".
	Kind   string `json:"kind"`
	Tool   string `json:"tool"`
	Detail string `json:"detail"`
	// Note is the agent's own description of the command in Detail, to be
	// shown as its claim and never in place of the command.
	Note string `json:"note"`
	// Result is the beginning of a tool's result. Done is false while the
	// tool has not returned; Failed is true when it returned an error.
	Result string `json:"result"`
	Done   bool   `json:"done"`
	Failed bool   `json:"failed"`
	AtMs   int64  `json:"atMs"`
}

// Trail is the latest steps of a session's main thread or of one subagent.
type Trail struct {
	SessionID string      `json:"sessionId"`
	AgentID   string      `json:"agentId"`
	Steps     []TrailStep `json:"steps"`
	// Earlier is true when the transcript goes back further than Steps.
	Earlier bool `json:"earlier"`
	// Hidden is true when the contents were left out on request.
	Hidden bool `json:"hidden"`
	// Closed is true when the session has ended: what is shown is a record.
	Closed bool `json:"closed"`
}

// trailCache is the trail last handed out, with the state of the transcript
// it was read from. The panel asks again on every refresh; while the
// transcript has not grown, the answer is the same and the megabyte at its
// end is not read again.
type trailCache struct {
	path    string
	hide    bool
	version int64
	trail   Trail
}

// trailRef is the transcript a trail is read from.
type trailRef struct {
	path      string
	cwd       string
	sidechain bool
	closed    bool
	// version is the state of the transcript as the board last read it, or
	// zero when the board has not read it.
	version int64
}

// Trail returns the latest steps of a session's main thread, or of one of its
// subagents when agentID is given. The session must be on the board: running,
// or listed as closed today. It reads only the end of the transcript. With
// hide set, what was said and run is left out and only the kind of each step,
// its tool and its time remain.
func (s *Service) Trail(sessionID, agentID string, hide bool) (Trail, error) {
	out := Trail{SessionID: sessionID, AgentID: agentID, Steps: []TrailStep{}, Hidden: hide}
	ref, err := s.trailFile(sessionID, agentID)
	if err != nil {
		return out, err
	}
	out.Closed = ref.closed

	s.mu.Lock()
	if c := s.trail; ref.version != 0 && c.version == ref.version && c.path == ref.path && c.hide == hide &&
		c.trail.SessionID == sessionID && c.trail.AgentID == agentID && c.trail.Closed == ref.closed {
		s.mu.Unlock()
		return c.trail, nil
	}
	s.mu.Unlock()

	steps, earlier, err := parse.Trail(ref.path, ref.sidechain, trailBytes, trailSteps)
	if err != nil {
		return out, errors.New("không đọc được transcript của agent này")
	}
	out.Earlier = earlier
	for _, st := range steps {
		row := TrailStep{Kind: st.Kind, Tool: st.Tool, Done: st.Done, Failed: st.Failed, AtMs: ms(st.At)}
		if !hide {
			row.Detail, row.Note, row.Result = st.Detail, st.Note, st.Result
			if st.IsPath {
				row.Detail = relativeTo(ref.cwd, st.Detail)
			}
		}
		out.Steps = append(out.Steps, row)
	}
	if ref.version != 0 {
		s.mu.Lock()
		s.trail = trailCache{path: ref.path, hide: hide, version: ref.version, trail: out}
		s.mu.Unlock()
	}
	return out, nil
}

// versionOf is the state of a transcript as the board last read it. The
// caller holds s.mu.
func (s *Service) versionOf(path string) int64 {
	if e, ok := s.files[path]; ok {
		return e.file.Version()
	}
	return 0
}

// trailFile finds the transcript to read. Only sessions that are on the board
// are looked at, the running ones and the ones listed as closed today, so an
// id can never lead outside the transcripts the board already shows.
func (s *Service) trailFile(sessionID, agentID string) (trailRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configErr != nil {
		return trailRef{}, s.configErr
	}
	sessions, err := s.registry.Live(s.configDir, s.alive)
	if err != nil {
		return trailRef{}, errors.New("không đọc được danh sách session")
	}
	pick := func(main, cwd string, agents []scan.AgentFile, closed bool) (trailRef, error) {
		if agentID == "" {
			return trailRef{path: main, cwd: cwd, closed: closed, version: s.versionOf(main)}, nil
		}
		for _, af := range agents {
			if af.ID == agentID {
				return trailRef{path: af.Path, cwd: cwd, sidechain: true, closed: closed, version: s.versionOf(af.Path)}, nil
			}
		}
		return trailRef{}, errors.New("không tìm thấy agent này trong session")
	}
	for _, rs := range sessions {
		if rs.SessionID != sessionID {
			continue
		}
		main, ok := scan.TranscriptPath(s.configDir, rs)
		if !ok {
			return trailRef{}, errors.New("session này chưa có transcript")
		}
		var agents []scan.AgentFile
		if agentID != "" {
			agents = scan.AgentFiles(main)
		}
		return pick(main, rs.Cwd, agents, false)
	}
	if c, ok := s.closed[sessionID]; ok {
		return pick(c.path, c.cwd, c.agents, true)
	}
	return trailRef{}, errors.New("session này không còn trên bảng")
}

// Resumable reports the folder in which a session that closed today can be
// opened again. Only a session the board lists as closed qualifies: one that
// is still running already has a home. The folder is the one the session
// recorded, and it must be the folder its transcript is filed under, because
// that is where Claude Code will look for it.
func (s *Service) Resumable(sessionID string) (dir string, err error) {
	s.mu.Lock()
	c, ok := s.closed[sessionID]
	s.mu.Unlock()
	if !ok {
		return "", errors.New("session này không nằm trong danh sách đã đóng hôm nay")
	}
	if c.cwd == "" {
		return "", errors.New("transcript của session này không ghi nó đã chạy ở thư mục nào")
	}
	// Where capitals do not tell two folders apart they do not tell two
	// spellings of one folder apart either: "d:\x" and "D:\x" are filed
	// under the same folder there.
	slug, filed := scan.Slug(c.cwd), filepath.Base(filepath.Dir(c.path))
	same := slug == filed
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		same = strings.EqualFold(slug, filed)
	}
	if !same {
		return "", errors.New("session này đã đổi thư mục làm việc giữa chừng, nên không mở lại được từ app")
	}
	return c.cwd, nil
}
