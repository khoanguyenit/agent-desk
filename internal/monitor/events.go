package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// Event kinds.
const (
	// EventSessionIdle: a session's main thread stopped working and is
	// waiting for the user.
	EventSessionIdle = "session-idle"
	// EventAgentsDone: one or more subagents of a session finished while the
	// session itself carried on.
	EventAgentsDone = "agents-done"
)

// Event is something that finished between two snapshots.
type Event struct {
	Kind    string
	Project string
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
		title = e.Project + ": đã xong, đang chờ bạn"
		if e.Status != "idle" && e.Status != "" {
			title = e.Project + ": session chuyển sang " + e.Status
		}
		if !privacy && e.Session != "" {
			lines = append(lines, e.Session)
		}
		if n := len(e.Agents); n > 0 {
			lines = append(lines, fmt.Sprintf("%d agent vừa xong.", n))
		}
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

			wasStatus := map[string]string{}
			for _, a := range old.Agents {
				wasStatus[a.ID] = a.Status
			}
			var finished []string
			for _, a := range s.Agents {
				if a.Status != StatusDone {
					continue
				}
				// An agent seen for the first time and already done started
				// and finished between two refreshes.
				if was, seen := wasStatus[a.ID]; !seen || was != StatusDone {
					finished = append(finished, a.Description)
				}
			}

			switch {
			case old.Status == "busy" && s.Status != "busy":
				// The session coming to rest is the news; the agents that
				// finished along the way are part of it.
				events = append(events, Event{
					Kind: EventSessionIdle, Project: p.Name, Session: title,
					Status: s.Status, Agents: finished, StillActive: s.ActiveAgents,
				})
			case len(finished) > 0:
				events = append(events, Event{
					Kind: EventAgentsDone, Project: p.Name, Session: title,
					Agents: finished, StillActive: s.ActiveAgents,
				})
			}
		}
	}
	return events
}
