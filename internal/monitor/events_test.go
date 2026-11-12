package monitor

import (
	"reflect"
	"testing"
)

func snapshotWith(sessions ...Session) Snapshot {
	return Snapshot{Projects: []Project{{Name: "app", Sessions: sessions}}}
}

func agent(id, description, status string) Agent {
	return Agent{ID: id, Description: description, Status: status}
}

func TestDiff(t *testing.T) {
	busy := Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: "busy", ActiveAgents: 2,
		Agents: []Agent{agent("a", "Code review", StatusRunning), agent("b", "Go review", StatusQuiet), agent("c", "Old one", StatusDone)}}

	cases := []struct {
		name string
		prev Snapshot
		cur  Snapshot
		want []Event
	}{
		{
			name: "nothing changed",
			prev: snapshotWith(busy), cur: snapshotWith(busy),
		},
		{
			name: "the first snapshot is silent however much is already done",
			prev: Snapshot{}, cur: snapshotWith(busy),
		},
		{
			name: "a session new in this snapshot is silent",
			prev: snapshotWith(busy),
			cur:  snapshotWith(busy, Session{ID: "s2", PID: 2, Status: "idle", Agents: []Agent{agent("x", "Done already", StatusDone)}}),
		},
		{
			name: "one agent finishes while the session carries on",
			prev: snapshotWith(busy),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: "busy", ActiveAgents: 1,
				Agents: []Agent{agent("a", "Code review", StatusDone), agent("b", "Go review", StatusQuiet), agent("c", "Old one", StatusDone)}}),
			want: []Event{{Kind: EventAgentsDone, Project: "app", SessionID: "s1", Session: "Fix timeout", Agents: []string{"Code review"}, StillActive: 1}},
		},
		{
			name: "several agents finishing together are one event",
			prev: snapshotWith(busy),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: "busy",
				Agents: []Agent{agent("a", "Code review", StatusDone), agent("b", "Go review", StatusDone), agent("c", "Old one", StatusDone)}}),
			want: []Event{{Kind: EventAgentsDone, Project: "app", SessionID: "s1", Session: "Fix timeout", Agents: []string{"Code review", "Go review"}}},
		},
		{
			name: "the session coming to rest is the event, with the agents that finished along the way",
			prev: snapshotWith(busy),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: "idle", ActiveAgents: 1,
				Agents: []Agent{agent("a", "Code review", StatusDone), agent("b", "Go review", StatusQuiet), agent("c", "Old one", StatusDone)}}),
			want: []Event{{Kind: EventSessionIdle, Project: "app", SessionID: "s1", Session: "Fix timeout", Status: "idle", Agents: []string{"Code review"}, StillActive: 1}},
		},
		{
			name: "an agent that started and finished between two refreshes still counts",
			prev: snapshotWith(busy),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: "busy", ActiveAgents: 2,
				Agents: append([]Agent{agent("quick", "Quick lookup", StatusDone)}, busy.Agents...)}),
			want: []Event{{Kind: EventAgentsDone, Project: "app", SessionID: "s1", Session: "Fix timeout", Agents: []string{"Quick lookup"}, StillActive: 2}},
		},
		{
			name: "going from idle to busy is not an event",
			prev: snapshotWith(Session{ID: "s1", PID: 1, Status: "idle"}),
			cur:  snapshotWith(Session{ID: "s1", PID: 1, Status: "busy"}),
		},
		{
			name: "a second listing of the same session stays silent",
			prev: snapshotWith(busy, Session{ID: "s1", PID: 9, Status: "busy", Duplicate: true}),
			cur:  snapshotWith(busy, Session{ID: "s1", PID: 9, Status: "idle", Duplicate: true}),
		},
		{
			name: "a session with no title is named by its registry name",
			prev: snapshotWith(Session{ID: "s3", PID: 3, Name: "app-7", Status: "busy"}),
			cur:  snapshotWith(Session{ID: "s3", PID: 3, Name: "app-7", Status: "idle"}),
			want: []Event{{Kind: EventSessionIdle, Project: "app", SessionID: "s3", Session: "app-7", Status: "idle"}},
		},
		{
			name: "an agent given up on is said once, when it stops being counted as working",
			prev: snapshotWith(busy),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: "busy", ActiveAgents: 1,
				Agents: []Agent{agent("a", "Code review", StatusRunning), agent("b", "Go review", StatusStopped), agent("c", "Old one", StatusDone)}}),
			want: []Event{{Kind: EventAgentsStuck, Project: "app", SessionID: "s1", Session: "Fix timeout", Agents: []string{"Go review"}, StillActive: 1}},
		},
		{
			name: "an agent that was already stopped is not news",
			prev: snapshotWith(Session{ID: "s1", PID: 1, Status: "busy", Agents: []Agent{agent("b", "Go review", StatusStopped)}}),
			cur:  snapshotWith(Session{ID: "s1", PID: 1, Status: "busy", Agents: []Agent{agent("b", "Go review", StatusStopped)}}),
		},
		{
			name: "a long wait on one tool is said when it passes the mark",
			prev: snapshotWith(Session{ID: "s1", PID: 1, Title: "Build", Status: "busy", ActiveAgents: 1,
				Agents: []Agent{{ID: "a", Description: "Run the suite", Status: StatusQuiet, AwaitingTool: true, SilentMs: slowToolAfter.Milliseconds() - 1000}}}),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Title: "Build", Status: "busy", ActiveAgents: 1,
				Agents: []Agent{{ID: "a", Description: "Run the suite", Status: StatusQuiet, AwaitingTool: true, SilentMs: slowToolAfter.Milliseconds() + 1000}}}),
			want: []Event{{Kind: EventToolSlow, Project: "app", SessionID: "s1", Session: "Build", Agents: []string{"Run the suite"}, StillActive: 1}},
		},
		{
			name: "and not again after it",
			prev: snapshotWith(Session{ID: "s1", PID: 1, Status: "busy", ActiveAgents: 1,
				Agents: []Agent{{ID: "a", Description: "Run the suite", Status: StatusQuiet, AwaitingTool: true, SilentMs: slowToolAfter.Milliseconds() + 1000}}}),
			cur: snapshotWith(Session{ID: "s1", PID: 1, Status: "busy", ActiveAgents: 1,
				Agents: []Agent{{ID: "a", Description: "Run the suite", Status: StatusQuiet, AwaitingTool: true, SilentMs: slowToolAfter.Milliseconds() + 3000}}}),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Diff(c.prev, c.cur)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("events = %+v\n   want %+v", got, c.want)
			}
		})
	}
}

func TestBudgetCrossed(t *testing.T) {
	at := func(day string, usd float64) Snapshot {
		return Snapshot{Today: day, TodaySpend: Spend{CostUSD: usd}}
	}
	cases := []struct {
		name      string
		prev, cur Snapshot
		limit     float64
		want      bool
	}{
		{"going past the limit", at("2026-11-12", 9.5), at("2026-11-12", 10.1), 10, true},
		{"already past it", at("2026-11-12", 10.1), at("2026-11-12", 10.4), 10, false},
		{"still under it", at("2026-11-12", 3), at("2026-11-12", 4), 10, false},
		{"no limit set", at("2026-11-12", 9.5), at("2026-11-12", 10.1), 0, false},
		{"the first snapshot after start-up", Snapshot{}, at("2026-11-12", 12), 10, false},
		{"a new day is not a crossing", at("2026-11-11", 2), at("2026-11-12", 11), 10, false},
	}
	for _, c := range cases {
		if got := BudgetCrossed(c.prev, c.cur, c.limit); got != c.want {
			t.Errorf("%s: BudgetCrossed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEventText(t *testing.T) {
	cases := []struct {
		name      string
		event     Event
		privacy   bool
		wantTitle string
		wantBody  string
	}{
		{
			name:      "session at rest",
			event:     Event{Kind: EventSessionIdle, Project: "app", Session: "Fix timeout", Status: "idle"},
			wantTitle: "app: đã xong, đang chờ bạn",
			wantBody:  "Fix timeout",
		},
		{
			name:      "session at rest with agents",
			event:     Event{Kind: EventSessionIdle, Project: "app", Session: "Fix timeout", Status: "idle", Agents: []string{"A", "B"}, StillActive: 1},
			wantTitle: "app: đã xong, đang chờ bạn",
			wantBody:  "Fix timeout\n2 agent vừa xong.\nCòn 1 agent đang chạy.",
		},
		{
			name:      "an unfamiliar status is shown as it is",
			event:     Event{Kind: EventSessionIdle, Project: "app", Session: "Fix timeout", Status: "waiting"},
			wantTitle: "app: session chuyển sang waiting",
			wantBody:  "Fix timeout",
		},
		{
			name:      "agents done",
			event:     Event{Kind: EventAgentsDone, Project: "app", Session: "Fix timeout", Agents: []string{"Code review", "Go review"}, StillActive: 3},
			wantTitle: "app: 2 agent đã xong",
			wantBody:  "Code review, Go review\nCòn 3 agent đang chạy.",
		},
		{
			name:      "a long list is cut",
			event:     Event{Kind: EventAgentsDone, Project: "app", Agents: []string{"A", "B", "C", "D", "E"}},
			wantTitle: "app: 5 agent đã xong",
			wantBody:  "A, B, C và 2 agent khác",
		},
		{
			name:      "privacy keeps titles and descriptions out",
			event:     Event{Kind: EventSessionIdle, Project: "app", Session: "Fix timeout", Status: "idle", Agents: []string{"Code review"}, StillActive: 1},
			privacy:   true,
			wantTitle: "app: đã xong, đang chờ bạn",
			wantBody:  "1 agent vừa xong.\nCòn 1 agent đang chạy.",
		},
		{
			name:      "agents given up on",
			event:     Event{Kind: EventAgentsStuck, Project: "app", Agents: []string{"Go review"}, StillActive: 2},
			wantTitle: "app: 1 agent có vẻ đã dừng giữa chừng",
			wantBody:  "Go review\nIm lặng đã lâu mà chưa báo xong.\nCòn 2 agent đang chạy.",
		},
		{
			name:      "a slow tool, with privacy on",
			event:     Event{Kind: EventToolSlow, Project: "app", Agents: []string{"Run the suite"}},
			privacy:   true,
			wantTitle: "app: 1 agent chờ một lệnh đã 30 phút",
			wantBody:  "Lệnh chạy lâu là bình thường; nếu không phải vậy thì có thể nó đã treo.",
		},
		{
			name:      "privacy on agents done",
			event:     Event{Kind: EventAgentsDone, Project: "app", Agents: []string{"Code review"}},
			privacy:   true,
			wantTitle: "app: 1 agent đã xong",
			wantBody:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, body := c.event.Text(c.privacy)
			if title != c.wantTitle || body != c.wantBody {
				t.Fatalf("title = %q, body = %q\nwant title = %q, body = %q", title, body, c.wantTitle, c.wantBody)
			}
		})
	}
}
