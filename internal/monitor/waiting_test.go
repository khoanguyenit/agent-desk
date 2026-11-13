package monitor

import (
	"strings"
	"testing"
	"time"
)

func waitingSnapshot(atMs, sinceMs int64, status string, act Activity) Snapshot {
	s := Session{ID: "s1", PID: 1, Title: "Chọn cơ sở dữ liệu", Status: status, StatusSinceMs: sinceMs, Activity: act}
	return Snapshot{GeneratedAtMs: atMs, Projects: []Project{{Name: "app", Path: `d:\app`, Sessions: []Session{s}}}}
}

func TestAWaitingSessionIsMentionedOnceMoreAfterTenMinutes(t *testing.T) {
	since := fixedNow.UnixMilli()
	ask := Activity{Kind: "tool", Tool: "AskUserQuestion", Detail: "Dùng MySQL hay PostgreSQL?"}

	// It starts to wait: the usual notice, which now says what it asks.
	events := Diff(waitingSnapshot(since-2000, 0, SessionBusy, ask), waitingSnapshot(since, since, SessionWaiting, ask))
	if len(events) != 1 || events[0].Kind != EventSessionIdle {
		t.Fatalf("events = %+v", events)
	}
	if _, body := events[0].Text(false); !strings.Contains(body, "Hỏi: Dùng MySQL hay PostgreSQL?") {
		t.Errorf("body = %q", body)
	}
	if _, body := events[0].Text(true); strings.Contains(body, "MySQL") {
		t.Errorf("privacy kept the question: %q", body)
	}

	mark := since + remindAfter.Milliseconds()
	// Nothing while the wait is short, then once as it passes the mark,
	// then nothing again.
	if ev := Diff(waitingSnapshot(mark-4000, since, SessionWaiting, ask), waitingSnapshot(mark-2000, since, SessionWaiting, ask)); len(ev) != 0 {
		t.Fatalf("before the mark: %+v", ev)
	}
	ev := Diff(waitingSnapshot(mark-1000, since, SessionWaiting, ask), waitingSnapshot(mark+1000, since, SessionWaiting, ask))
	if len(ev) != 1 || ev[0].Kind != EventStillWaiting {
		t.Fatalf("at the mark: %+v", ev)
	}
	title, body := ev[0].Text(false)
	if title != "app: vẫn chờ bạn sau 10 phút" || !strings.Contains(body, "Chọn cơ sở dữ liệu") || !strings.Contains(body, "Hỏi: Dùng MySQL") {
		t.Errorf("text = %q / %q", title, body)
	}
	if ev := Diff(waitingSnapshot(mark+1000, since, SessionWaiting, ask), waitingSnapshot(mark+3000, since, SessionWaiting, ask)); len(ev) != 0 {
		t.Fatalf("after the mark: %+v", ev)
	}

	// A new wait, begun after the user answered once, is a wait of its own.
	later := mark + time.Minute.Milliseconds()
	if ev := Diff(waitingSnapshot(later-1000, since, SessionWaiting, ask), waitingSnapshot(mark+1000+remindAfter.Milliseconds(), later, SessionWaiting, ask)); len(ev) != 0 {
		t.Fatalf("a different wait counted from the old start: %+v", ev)
	}
}
