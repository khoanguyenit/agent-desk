package monitor

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event kinds.
const (
	// EventSessionIdle: a session's main thread stopped working. Either its
	// turn is over, or it has stopped in the middle of one because it needs
	// the user; Event.Status says which.
	EventSessionIdle = "session-idle"
	// EventAgentsDone: one or more subagents of a session finished while the
	// session itself carried on.
	EventAgentsDone = "agents-done"
	// EventAgentsStuck: one or more subagents that were working are now
	// counted as stopped: they fell silent without finishing.
	EventAgentsStuck = "agents-stuck"
	// EventToolSlow: one or more subagents have been waiting on a single tool
	// call for a long time. It is said once, when the wait passes the mark.
	EventToolSlow = "tool-slow"
)

// slowToolAfter is how long an agent waits on one tool call before the user
// is told. Builds and test suites take minutes; half an hour is unusual.
const slowToolAfter = 30 * time.Minute

// Event is something that finished between two snapshots.
type Event struct {
	Kind    string
	Project string
	// ProjectPath and SessionID say where the event happened, so that a
	// click on its notification can lead there. They are never shown.
	ProjectPath string
	SessionID   string
	// Session is the session's title, or its name when it has no title yet.
	Session string
	// Status is the session's new status for EventSessionIdle.
	Status string
	// Agents are the descriptions of the agents that finished.
	Agents []string
	// StillActive is how many of the session's agents are still working.
	StillActive int
}

// Text words the event for a desktop notification. With privacy on, the
// project is still named (the user needs to know where to look) but session
// titles and agent descriptions, which summarise the work, are left out.
func (e Event) Text(privacy bool) (title, body string) {
	var lines []string
	switch e.Kind {
	case EventSessionIdle:
		switch e.Status {
		case SessionIdle, "":
			title = e.Project + ": đã xong, đang chờ bạn"
		case SessionWaiting:
			// Not finished: held up until the user answers or approves.
			title = e.Project + ": cần bạn trả lời"
		default:
			title = e.Project + ": session chuyển sang " + e.Status
		}
		if !privacy && e.Session != "" {
			lines = append(lines, e.Session)
		}
		if e.Status == SessionWaiting {
			lines = append(lines, "Claude Code đang dừng lại chờ bạn trả lời hoặc duyệt.")
		}
		if n := len(e.Agents); n > 0 {
			lines = append(lines, fmt.Sprintf("%d agent vừa xong.", n))
		}
	case EventAgentsStuck:
		title = fmt.Sprintf("%s: %d agent có vẻ đã dừng giữa chừng", e.Project, len(e.Agents))
		if !privacy {
			lines = append(lines, listNames(e.Agents, 3))
		}
		lines = append(lines, "Im lặng đã lâu mà chưa báo xong.")
	case EventToolSlow:
		title = fmt.Sprintf("%s: %d agent chờ một lệnh đã %d phút", e.Project, len(e.Agents), int(slowToolAfter.Minutes()))
		if !privacy {
			lines = append(lines, listNames(e.Agents, 3))
		}
		lines = append(lines, "Lệnh chạy lâu là bình thường; nếu không phải vậy thì có thể nó đã treo.")
	default:
		title = fmt.Sprintf("%s: %d agent đã xong", e.Project, len(e.Agents))
		if !privacy {
			lines = append(lines, listNames(e.Agents, 3))
		}
	}
	if e.StillActive > 0 {
		lines = append(lines, fmt.Sprintf("Còn %d agent đang chạy.", e.StillActive))
	}
	return title, strings.Join(lines, "\n")
}

func listNames(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s và %d agent khác", strings.Join(names[:limit], ", "), len(names)-limit)
}

func sessionKey(s Session) string { return s.ID + "#" + strconv.Itoa(s.PID) }

// BudgetCrossed reports whether today's cost went past limitUSD between two
// snapshots of the same day. The first snapshot after start-up never counts,
// and neither does a new day: only the moment of crossing is news.
func BudgetCrossed(prev, cur Snapshot, limitUSD float64) bool {
	return limitUSD > 0 && prev.Today != "" && prev.Today == cur.Today &&
		prev.TodaySpend.CostUSD < limitUSD && cur.TodaySpend.CostUSD >= limitUSD
}

// Diff reports what finished between prev and cur. A session that is new in
// cur produces nothing: there is no earlier state to compare it with, so the
// first snapshot after start-up is silent however much has already happened.
func Diff(prev, cur Snapshot) []Event {
	before := map[string]Session{}
	for _, p := range prev.Projects {
		for _, s := range p.Sessions {
			before[sessionKey(s)] = s
		}
	}

	var events []Event
	for _, p := range cur.Projects {
		for _, s := range p.Sessions {
			old, known := before[sessionKey(s)]
			if !known || s.Duplicate {
				continue
			}
			title := s.Title
			if title == "" {
				title = s.Name
			}

			wasAgent := map[string]Agent{}
			for _, a := range old.Agents {
				wasAgent[a.ID] = a
			}
			var finished, stuck, slow []string
			for _, a := range s.Agents {
				was, seen := wasAgent[a.ID]
				switch {
				case a.Status == StatusDone:
					// An agent seen for the first time and already done
					// started and finished between two refreshes.
					if !seen || was.Status != StatusDone {
						finished = append(finished, a.Description)
					}
				case a.Status == StatusStopped:
					// Only the moment it is given up on is news. One that
					// was already stopped when first seen is history.
					if seen && (was.Status == StatusRunning || was.Status == StatusQuiet) {
						stuck = append(stuck, a.Description)
					}
				case a.AwaitingTool:
					if seen && was.AwaitingTool && was.SilentMs < slowToolAfter.Milliseconds() && a.SilentMs >= slowToolAfter.Milliseconds() {
						slow = append(slow, a.Description)
					}
				}
			}

			at := Event{Project: p.Name, ProjectPath: p.Path, SessionID: s.ID, Session: title, StillActive: s.ActiveAgents}
			with := func(kind string, agents []string) Event {
				e := at
				e.Kind, e.Agents = kind, agents
				return e
			}
			switch {
			case old.Status == "busy" && s.Status != "busy":
				// The session coming to rest is the news; the agents that
				// finished along the way are part of it.
				e := with(EventSessionIdle, finished)
				e.Status = s.Status
				events = append(events, e)
			case len(finished) > 0:
				events = append(events, with(EventAgentsDone, finished))
			}
			if len(stuck) > 0 {
				events = append(events, with(EventAgentsStuck, stuck))
			}
			if len(slow) > 0 {
				events = append(events, with(EventToolSlow, slow))
			}
		}
	}
	return events
}
