package monitor

import (
	"fmt"
	"slices"
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
	// EventLimited: sessions or agents were refused for a usage limit. One
	// event per limit, however many projects it stops.
	EventLimited = "limited"
	// EventLimitLifted: the time a limit named for lifting has come.
	EventLimitLifted = "limit-lifted"
	// EventStillWaiting: a session has waited for the user for a while and
	// nobody has answered. It is said once per wait, when it passes the mark.
	EventStillWaiting = "still-waiting"
)

// remindAfter is how long a session waits for the user before it is
// mentioned a second time. The first mention may have come while the user
// was looking elsewhere.
const remindAfter = 10 * time.Minute

// eventZone is the time zone the times in notifications are written in.
var eventZone = time.Local

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
	// Question is what a waiting session asks the user, when it asked with
	// AskUserQuestion. It is the work's own words, kept out under privacy.
	Question string
	// Agents are the descriptions of the agents that finished.
	Agents []string
	// StillActive is how many of the session's agents are still working.
	StillActive int
	// For EventLimited and EventLimitLifted: the limit, how many sessions'
	// main threads it holds, and the projects it holds them in. The agents
	// it holds are in Agents.
	Limit    *Limit
	Sessions int
	Projects []string
	// NowMs is when the event was noticed.
	NowMs int64
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
			if !privacy && e.Question != "" {
				lines = append(lines, "Hỏi: "+e.Question)
			} else {
				lines = append(lines, "Claude Code đang dừng lại chờ bạn trả lời hoặc duyệt.")
			}
		}
		if n := len(e.Agents); n > 0 {
			lines = append(lines, fmt.Sprintf("%d agent vừa xong.", n))
		}
	case EventStillWaiting:
		title = fmt.Sprintf("%s: vẫn chờ bạn sau %d phút", e.Project, int(remindAfter.Minutes()))
		if !privacy && e.Session != "" {
			lines = append(lines, e.Session)
		}
		if !privacy && e.Question != "" {
			lines = append(lines, "Hỏi: "+e.Question)
		} else {
			lines = append(lines, "Chưa ai trả lời hoặc duyệt yêu cầu của session này.")
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
	case EventLimited:
		title = "Chạm " + LimitName(e.Limit.Kind)
		if held := heldText(e.Sessions, len(e.Agents)); held != "" {
			lines = append(lines, held+" phải dừng ở "+strings.Join(e.Projects, ", ")+".")
		}
		if !privacy && len(e.Agents) > 0 {
			lines = append(lines, listNames(e.Agents, 3))
		}
		if e.Limit.ResetsAtMs > 0 {
			lines = append(lines, "Mở lại lúc "+WhenText(e.Limit.ResetsAtMs, e.NowMs)+".")
		} else {
			lines = append(lines, "Claude Code không ghi khi nào giới hạn mở lại.")
		}
	case EventLimitLifted:
		title = upperFirst(LimitName(e.Limit.Kind)) + " đã mở lại"
		lines = append(lines, heldText(e.Sessions, len(e.Agents))+
			" bị chặn ở "+strings.Join(e.Projects, ", ")+" không tự chạy lại: hãy bảo session làm tiếp.")
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

// LimitName words a usage limit by Claude Code's name for it.
func LimitName(kind string) string {
	switch {
	case kind == "":
		return "giới hạn sử dụng"
	case strings.HasPrefix(kind, "five_hour"):
		return "giới hạn 5 giờ"
	case strings.HasPrefix(kind, "seven_day"):
		return "giới hạn tuần"
	default:
		return "giới hạn " + kind
	}
}

var weekdays = [...]string{"Chủ nhật", "thứ hai", "thứ ba", "thứ tư", "thứ năm", "thứ sáu", "thứ bảy"}

// WhenText words a moment near nowMs: "17:00 hôm nay", "02:00 ngày mai",
// or "02:00 thứ sáu 13/11" further off.
func WhenText(atMs, nowMs int64) string {
	t := time.UnixMilli(atMs).In(eventZone)
	n := time.UnixMilli(nowMs).In(eventZone)
	day := func(x time.Time) string { return x.Format("2006-01-02") }
	clock := t.Format("15:04")
	switch day(t) {
	case day(n):
		return clock + " hôm nay"
	case day(n.AddDate(0, 0, 1)):
		return clock + " ngày mai"
	default:
		return fmt.Sprintf("%s %s %d/%d", clock, weekdays[t.Weekday()], t.Day(), int(t.Month()))
	}
}

// heldText says how much a limit holds: "2 session và 12 agent".
func heldText(sessions, agents int) string {
	var parts []string
	if sessions > 0 {
		parts = append(parts, fmt.Sprintf("%d session", sessions))
	}
	if agents > 0 {
		parts = append(parts, fmt.Sprintf("%d agent", agents))
	}
	return strings.Join(parts, " và ")
}

func upperFirst(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
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
			if s.Status == SessionWaiting && s.Activity.Kind == "tool" && s.Activity.Tool == "AskUserQuestion" {
				at.Question = s.Activity.Detail
			}
			with := func(kind string, agents []string) Event {
				e := at
				e.Kind, e.Agents = kind, agents
				return e
			}
			// A session stopped by a usage limit has not finished its turn:
			// the limit's own notice says what happened.
			stoppedByLimit := old.Limit == nil && s.Limit != nil
			switch {
			case old.Status == "busy" && s.Status != "busy" && !stoppedByLimit:
				// The session coming to rest is the news; the agents that
				// finished along the way are part of it.
				e := with(EventSessionIdle, finished)
				e.Status = s.Status
				events = append(events, e)
			case len(finished) > 0:
				events = append(events, with(EventAgentsDone, finished))
			}
			// Still waiting for the user, the same wait as before, and it
			// has just passed the mark: say it once more.
			if s.Status == SessionWaiting && old.Status == SessionWaiting && s.StatusSinceMs > 0 && s.StatusSinceMs == old.StatusSinceMs {
				mark := s.StatusSinceMs + remindAfter.Milliseconds()
				if prev.GeneratedAtMs < mark && cur.GeneratedAtMs >= mark {
					events = append(events, with(EventStillWaiting, nil))
				}
			}
			if len(stuck) > 0 {
				events = append(events, with(EventAgentsStuck, stuck))
			}
			if len(slow) > 0 {
				events = append(events, with(EventToolSlow, slow))
			}
		}
	}
	return append(events, limitEvents(prev, cur)...)
}

type limitKey struct {
	kind string
	at   int64
}

// limitEvents reports the usage limits that came into force between two
// snapshots, and the ones whose time to lift came. Each limit is told once
// when it is first hit, however many sessions and agents run into it after,
// and again only if it lifted and is hit anew.
func limitEvents(prev, cur Snapshot) []Event {
	before := map[limitKey]bool{}
	for _, g := range prev.Limits {
		before[limitKey{g.Kind, g.ResetsAtMs}] = true
	}
	now := map[limitKey]bool{}
	for _, g := range cur.Limits {
		now[limitKey{g.Kind, g.ResetsAtMs}] = true
	}

	var events []Event
	held := holders(cur)
	for _, g := range cur.Limits {
		k := limitKey{g.Kind, g.ResetsAtMs}
		if before[k] {
			continue
		}
		if e := held[k]; e != nil {
			e.Kind, e.NowMs = EventLimited, cur.GeneratedAtMs
			events = append(events, *e)
		}
	}
	held = holders(prev)
	for _, g := range prev.Limits {
		k := limitKey{g.Kind, g.ResetsAtMs}
		if now[k] || g.ResetsAtMs == 0 || g.ResetsAtMs <= prev.GeneratedAtMs || g.ResetsAtMs > cur.GeneratedAtMs {
			// Still holding, or lifted early because the account was
			// answered again: then the user is already back at work.
			continue
		}
		if e := held[k]; e != nil {
			e.Kind, e.NowMs = EventLimitLifted, cur.GeneratedAtMs
			events = append(events, *e)
		}
	}
	return events
}

// holders gathers what each usage limit holds in a snapshot: the sessions'
// main threads, the agents, and the projects they are in.
func holders(snap Snapshot) map[limitKey]*Event {
	out := map[limitKey]*Event{}
	note := func(l *Limit, p Project) *Event {
		k := limitKey{l.Kind, l.ResetsAtMs}
		e, ok := out[k]
		if !ok {
			e = &Event{Limit: l, ProjectPath: p.Path}
			out[k] = e
		}
		if !slices.Contains(e.Projects, p.Name) {
			e.Projects = append(e.Projects, p.Name)
		}
		if e.ProjectPath != p.Path {
			// More than one project: a click leads to none in particular.
			e.ProjectPath = ""
		}
		return e
	}
	for _, p := range snap.Projects {
		for _, s := range p.Sessions {
			if s.Duplicate {
				continue
			}
			if s.Limit != nil {
				note(s.Limit, p).Sessions++
			}
			for _, a := range s.Agents {
				if a.Limit != nil {
					e := note(a.Limit, p)
					e.Agents = append(e.Agents, a.Description)
				}
			}
		}
	}
	return out
}
