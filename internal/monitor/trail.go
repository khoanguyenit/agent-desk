package monitor

import (
	"errors"

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
}

// Trail returns the latest steps of a running session's main thread, or of
// one of its subagents when agentID is given. It reads only the end of the
// transcript. With hide set, what was said and run is left out and only the
// kind of each step, its tool and its time remain.
func (s *Service) Trail(sessionID, agentID string, hide bool) (Trail, error) {
	out := Trail{SessionID: sessionID, AgentID: agentID, Steps: []TrailStep{}, Hidden: hide}
	path, cwd, sidechain, err := s.trailFile(sessionID, agentID)
	if err != nil {
		return out, err
	}
	steps, earlier, err := parse.Trail(path, sidechain, trailBytes, trailSteps)
	if err != nil {
		return out, errors.New("không đọc được transcript của agent này")
	}
	out.Earlier = earlier
	for _, st := range steps {
		row := TrailStep{Kind: st.Kind, Tool: st.Tool, Done: st.Done, Failed: st.Failed, AtMs: ms(st.At)}
		if !hide {
			row.Detail, row.Result = st.Detail, st.Result
			if st.IsPath {
				row.Detail = relativeTo(cwd, st.Detail)
			}
		}
		out.Steps = append(out.Steps, row)
	}
	return out, nil
}

// trailFile finds the transcript to read. Only sessions that are running now
// are looked at, so an id can never lead outside the transcripts the board
// already shows.
func (s *Service) trailFile(sessionID, agentID string) (path, cwd string, sidechain bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configErr != nil {
		return "", "", false, s.configErr
	}
	sessions, err := s.registry.Live(s.configDir, s.alive)
	if err != nil {
		return "", "", false, errors.New("không đọc được danh sách session")
	}
	for _, rs := range sessions {
		if rs.SessionID != sessionID {
			continue
		}
		main, ok := scan.TranscriptPath(s.configDir, rs)
		if !ok {
			return "", "", false, errors.New("session này chưa có transcript")
		}
		if agentID == "" {
			return main, rs.Cwd, false, nil
		}
		for _, af := range scan.AgentFiles(main) {
			if af.ID == agentID {
				return af.Path, rs.Cwd, true, nil
			}
		}
		return "", "", false, errors.New("không tìm thấy agent này trong session")
	}
	return "", "", false, errors.New("session này không còn chạy")
}
