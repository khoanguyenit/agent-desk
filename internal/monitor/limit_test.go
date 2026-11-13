package monitor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// limitReset is when the weekly limit in these tests lifts: 02:00 on the 13th
// in UTC+7, the morning after the fixture's "now".
var limitReset = time.Date(2026, 11, 12, 19, 0, 0, 0, time.UTC)

// refusalLine is the line Claude Code writes in the model's place when a
// usage limit is used up.
func refusalLine(ts, kind string, resetsAt time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"timestamp":%q,"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429,"apiErrorParams":{"rate_limit_info":{"status":"rejected","rateLimitType":%q,"resetsAt":%d}},"message":{"id":"msg_refused","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","content":[{"type":"text","text":"You've hit your weekly limit"}],"usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`+"\n",
		ts, kind, resetsAt.Unix())
}

func ownerLine(account string) string {
	return fmt.Sprintf(`{"type":"session-owner","accountUuid":%q,"organizationUuid":"org"}`+"\n", account)
}

// limitedWorld is the fixture with session sid-a on account acct-a and one of
// its agents refused for the weekly limit at 09:59:45 local time, after
// everything else on the account was last answered.
func limitedWorld(t *testing.T) world {
	t.Helper()
	w := fixture(t)
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"), ownerLine("acct-a"))
	put(t, filepath.Join(w.sub, "agent-held.jsonl"),
		msg("msg_h1", "claude-fable-5-2", "2026-11-12T02:50:00Z", "", 1, 10, 0, 1000)+
			refusalLine("2026-11-12T02:59:45Z", "seven_day", limitReset))
	put(t, filepath.Join(w.sub, "agent-held.meta.json"), `{"agentType":"workflow-subagent","description":"map:transcripts"}`)
	return w
}

func TestUsageLimitHoldsAnAgent(t *testing.T) {
	w := limitedWorld(t)
	snap := w.svc.Snapshot()
	a := findAgent(t, sessionA(t, snap), "held")
	if a.Status != StatusLimited || a.Limit == nil || a.Limit.Kind != "seven_day" || a.Limit.ResetsAtMs != limitReset.UnixMilli() {
		t.Fatalf("agent = status %q, limit %+v", a.Status, a.Limit)
	}
	if len(snap.Limits) != 1 || snap.Limits[0] != (LimitGroup{Kind: "seven_day", ResetsAtMs: limitReset.UnixMilli(), Agents: 1}) {
		t.Fatalf("limits = %+v", snap.Limits)
	}
	// Held is neither working nor given up on.
	if snap.Counts.QuietAgents != 1 || snap.Counts.RunningAgents != 1 {
		t.Errorf("counts = %+v; the held agent must not be counted as working", snap.Counts)
	}
	if s := sessionA(t, snap); s.Limit != nil {
		t.Errorf("the main thread was answered and is not held: %+v", s.Limit)
	}
}

// When a session on the same account is answered after the limit was hit,
// the limit has lifted: the user reset it, or its time came early.
func TestUsageLimitLiftsWhenTheAccountIsAnsweredAgain(t *testing.T) {
	w := limitedWorld(t)
	w.svc.Snapshot()
	appendTo(t, filepath.Join(w.project, "sid-b.jsonl"),
		ownerLine("acct-a")+msg("msg_b2", "claude-sonnet-5-5", "2026-11-12T02:59:50Z", "", 1, 1, 0, 0))
	snap := w.svc.Snapshot()
	a := findAgent(t, sessionA(t, snap), "held")
	if a.Limit != nil || a.Status != StatusRunning {
		t.Fatalf("agent = status %q, limit %+v; the account was answered after the limit", a.Status, a.Limit)
	}
	if len(snap.Limits) != 0 || snap.Counts.RunningAgents != 2 {
		t.Fatalf("limits = %+v, counts = %+v", snap.Limits, snap.Counts)
	}
}

// Another account being answered says nothing about this account's limit.
func TestUsageLimitIsPerAccount(t *testing.T) {
	w := limitedWorld(t)
	appendTo(t, filepath.Join(w.project, "sid-b.jsonl"),
		ownerLine("acct-b")+msg("msg_b2", "claude-sonnet-5-5", "2026-11-12T02:59:50Z", "", 1, 1, 0, 0))
	snap := w.svc.Snapshot()
	if a := findAgent(t, sessionA(t, snap), "held"); a.Status != StatusLimited {
		t.Fatalf("agent = %q; a session on another account was answered, not this one", a.Status)
	}
}

func TestUsageLimitLiftsWhenItsTimeComes(t *testing.T) {
	w := limitedWorld(t)
	svc := New(Options{
		ConfigDir: w.dir, PricingPath: filepath.Join(w.dir, "no-override.json"),
		Alive: func(pid int, _ string) bool { return pid != 300 },
		Now:   func() time.Time { return limitReset }, Location: ict,
	})
	snap := svc.Snapshot()
	if a := findAgent(t, sessionA(t, snap), "held"); a.Limit != nil || a.Status == StatusLimited {
		t.Fatalf("agent = status %q, limit %+v at the time the limit lifts", a.Status, a.Limit)
	}
	if len(snap.Limits) != 0 {
		t.Fatalf("limits = %+v", snap.Limits)
	}
}

func limitSnapshot(atMs int64, sessionStatus string, sessionLimit, agentLimit *Limit) Snapshot {
	s := Session{ID: "s1", PID: 1, Title: "Fix timeout", Status: sessionStatus, Limit: sessionLimit}
	if agentLimit != nil {
		s.Agents = []Agent{{ID: "a1", Description: "map:transcripts", Status: StatusLimited, Limit: agentLimit}}
	}
	snap := Snapshot{GeneratedAtMs: atMs, Today: "2026-11-12", Projects: []Project{{Name: "app", Path: `d:\app`, Sessions: []Session{s}}}}
	held := holders(snap)
	for k, e := range held {
		snap.Limits = append(snap.Limits, LimitGroup{Kind: k.kind, ResetsAtMs: k.at, Sessions: e.Sessions, Agents: len(e.Agents)})
	}
	return snap
}

func TestLimitEventsAreToldOnceAndAgainWhenTheyLift(t *testing.T) {
	old := eventZone
	eventZone = ict
	t.Cleanup(func() { eventZone = old })

	at := fixedNow.UnixMilli()
	weekly := &Limit{Kind: "seven_day", ResetsAtMs: limitReset.UnixMilli(), AtMs: at}

	// The main thread is refused while busy: Claude Code calls it idle, but
	// the turn did not finish, and the limit's notice is the one to give.
	before := limitSnapshot(at-2000, SessionBusy, nil, nil)
	hit := limitSnapshot(at, SessionIdle, weekly, weekly)
	events := Diff(before, hit)
	if len(events) != 1 || events[0].Kind != EventLimited {
		t.Fatalf("events = %+v", events)
	}
	title, body := events[0].Text(false)
	if title != "Chạm giới hạn tuần" ||
		!strings.Contains(body, "1 session và 1 agent phải dừng ở app.") ||
		!strings.Contains(body, "map:transcripts") ||
		!strings.Contains(body, "Mở lại lúc 02:00 ngày mai.") {
		t.Fatalf("text = %q / %q", title, body)
	}
	if _, body := events[0].Text(true); strings.Contains(body, "map:transcripts") {
		t.Errorf("privacy kept the agent's description: %q", body)
	}
	if events[0].ProjectPath != `d:\app` {
		t.Errorf("a click should lead to the one project held: %q", events[0].ProjectPath)
	}

	// Still held on the next scan: nothing new to say.
	if events := Diff(hit, limitSnapshot(at+2000, SessionIdle, weekly, weekly)); len(events) != 0 {
		t.Fatalf("told twice: %+v", events)
	}

	// Its time comes: the board drops it, and the user is told.
	lifted := limitSnapshot(limitReset.UnixMilli(), SessionIdle, nil, nil)
	events = Diff(limitSnapshot(limitReset.UnixMilli()-2000, SessionIdle, weekly, weekly), lifted)
	if len(events) != 1 || events[0].Kind != EventLimitLifted {
		t.Fatalf("events = %+v", events)
	}
	title, body = events[0].Text(false)
	if title != "Giới hạn tuần đã mở lại" || !strings.Contains(body, "1 session và 1 agent bị chặn ở app không tự chạy lại") {
		t.Fatalf("text = %q / %q", title, body)
	}

	// Lifted early because the account was answered again: the user is back
	// at work already, and is not told.
	if events := Diff(limitSnapshot(at+4000, SessionIdle, weekly, weekly), limitSnapshot(at+6000, SessionBusy, nil, nil)); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
}

func TestWhenText(t *testing.T) {
	old := eventZone
	eventZone = ict
	t.Cleanup(func() { eventZone = old })
	now := fixedNow.UnixMilli() // 10:00 on Thursday 12 Nov, UTC+7
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 12, 17, 0, 0, 0, ict), "17:00 hôm nay"},
		{time.Date(2026, 11, 13, 2, 0, 0, 0, ict), "02:00 ngày mai"},
		{time.Date(2026, 11, 16, 2, 0, 0, 0, ict), "02:00 thứ hai 16/11"},
	}
	for _, c := range cases {
		if got := WhenText(c.at.UnixMilli(), now); got != c.want {
			t.Errorf("WhenText(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}
