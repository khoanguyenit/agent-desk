package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cwdLine is a transcript line of the kind that records where a session ran.
func cwdLine(ts, cwd string) string {
	raw, _ := json.Marshal(map[string]any{
		"type": "user", "timestamp": ts, "cwd": cwd,
		"message": map[string]any{"role": "user", "content": "tiếp tục"},
	})
	return string(raw) + "\n"
}

func projectNamed(t *testing.T, snap Snapshot, name string) Project {
	t.Helper()
	for _, p := range snap.Projects {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no project named %q in %d projects", name, len(snap.Projects))
	return Project{}
}

// The reason this exists: closing a project must not make what it cost today
// disappear from the totals.
func TestSessionClosedTodayStaysInTheTotals(t *testing.T) {
	w := fixture(t)
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"), cwdLine("2026-11-12T02:59:40Z", w.cwd))
	open := w.svc.Snapshot()
	a := sessionA(t, open)
	if open.Counts.ClosedSessions != 0 || open.ClosedToday.TotalTokens != 0 {
		t.Fatalf("nothing has closed yet: %+v", open.Counts)
	}

	// The session's process goes away, and its registry entry with it.
	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	closed := w.svc.Snapshot()

	if closed.All.CostUSD != open.All.CostUSD || closed.All.TotalTokens != open.All.TotalTokens {
		t.Errorf("total went from $%.6f / %d tokens to $%.6f / %d when the session closed",
			open.All.CostUSD, open.All.TotalTokens, closed.All.CostUSD, closed.All.TotalTokens)
	}
	if closed.TodaySpend.CostUSD != open.TodaySpend.CostUSD || closed.TodaySpend.TotalTokens != open.TodaySpend.TotalTokens {
		t.Errorf("today went from $%.6f to $%.6f when the session closed", open.TodaySpend.CostUSD, closed.TodaySpend.CostUSD)
	}
	if closed.Counts.Sessions != 2 || closed.Counts.ClosedSessions != 1 || closed.Counts.Agents != 0 {
		t.Errorf("counts = %+v; the closed session is counted apart, and its agents are no longer at work", closed.Counts)
	}
	if closed.ClosedAll.CostUSD != a.All.CostUSD || closed.ClosedToday.CostUSD != a.Today.CostUSD {
		t.Errorf("closed part = $%.6f all, $%.6f today; the session was $%.6f and $%.6f",
			closed.ClosedAll.CostUSD, closed.ClosedToday.CostUSD, a.All.CostUSD, a.Today.CostUSD)
	}

	// It is listed under its project, beside the session still running there.
	app := projectNamed(t, closed, "app")
	if len(app.Sessions) != 1 || app.Sessions[0].ID != "sid-b" || len(app.Closed) != 1 {
		t.Fatalf("project app: %d running, %d closed", len(app.Sessions), len(app.Closed))
	}
	cs := app.Closed[0]
	if cs.ID != "sid-a" || cs.Title != "Fix timeout" || cs.ModelLabel != "Fable 5.2" || cs.Agents != 5 {
		t.Errorf("closed session = %+v", cs)
	}
	if cs.All.CostUSD != a.All.CostUSD || cs.Today.CostUSD != a.Today.CostUSD || cs.All.TotalTokens != a.All.TotalTokens {
		t.Errorf("closed session cost $%.6f / $%.6f today, was $%.6f / $%.6f while running", cs.All.CostUSD, cs.Today.CostUSD, a.All.CostUSD, a.Today.CostUSD)
	}
	if cs.EndedAtMs != time.Date(2026, 11, 12, 2, 59, 40, 0, time.UTC).UnixMilli() {
		t.Errorf("ended at %v, want the time of its last line", time.UnixMilli(cs.EndedAtMs).UTC())
	}
	if open.Projects[0].All.CostUSD != app.All.CostUSD {
		t.Errorf("the project's own total changed: $%.6f -> $%.6f", open.Projects[0].All.CostUSD, app.All.CostUSD)
	}

	// The totals per model do not move either.
	if len(closed.Models) != len(open.Models) {
		t.Fatalf("models: %d before, %d after", len(open.Models), len(closed.Models))
	}
	for i := range open.Models {
		if open.Models[i].Model != closed.Models[i].Model || open.Models[i].All.CostUSD != closed.Models[i].All.CostUSD ||
			open.Models[i].Today.CostUSD != closed.Models[i].Today.CostUSD {
			t.Errorf("model %s: $%.6f / $%.6f today became $%.6f / $%.6f", open.Models[i].Model,
				open.Models[i].All.CostUSD, open.Models[i].Today.CostUSD, closed.Models[i].All.CostUSD, closed.Models[i].Today.CostUSD)
		}
	}

	// Later refreshes say the same thing: the closed session is counted once.
	again := w.svc.Snapshot()
	if again.All.CostUSD != open.All.CostUSD || again.Counts.ClosedSessions != 1 {
		t.Errorf("on the next refresh: $%.6f, %d closed", again.All.CostUSD, again.Counts.ClosedSessions)
	}
}

// A session that went into a subfolder as it worked still belongs to the
// project it started in, which is where Claude Code filed its transcript and
// the only folder it can be resumed from.
func TestSessionThatMovedIntoASubfolderStaysWithItsProject(t *testing.T) {
	w := fixture(t)
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"),
		cwdLine("2026-11-12T02:59:30Z", w.cwd)+cwdLine("2026-11-12T02:59:40Z", filepath.Join(w.cwd, "frontend")))
	w.svc.Snapshot()
	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	closed := w.svc.Snapshot()

	app := projectNamed(t, closed, "app")
	if len(app.Closed) != 1 || app.Closed[0].ID != "sid-a" {
		t.Fatalf("project app has %d closed sessions; the session went elsewhere", len(app.Closed))
	}
	for _, p := range closed.Projects {
		if p.Name == "frontend" {
			t.Fatalf("the subfolder became a project of its own")
		}
	}
	dir, err := w.svc.Resumable("sid-a")
	if err != nil || dir != w.cwd {
		t.Fatalf("Resumable = %q, %v; want the folder it started in, %q", dir, err, w.cwd)
	}
}

// A project with nothing running any more stays on the page for the day, so
// that the projects still add up to the total.
func TestProjectWhoseOnlySessionClosedIsStillListed(t *testing.T) {
	w := fixture(t)
	other := filepath.Join(w.dir, "work", "other")
	put(t, filepath.Join(w.dir, "projects", "other-project", "sid-c.jsonl"),
		cwdLine("2026-11-12T02:00:00Z", other)+msg("msg_c1", "claude-sonnet-5-5", "2026-11-12T02:10:00Z", "", 10, 500, 0, 4000))
	open := w.svc.Snapshot()
	if err := os.Remove(filepath.Join(w.dir, "sessions", "400.json")); err != nil {
		t.Fatal(err)
	}
	closed := w.svc.Snapshot()

	p := projectNamed(t, closed, "other")
	if len(p.Sessions) != 0 || p.Sessions == nil || len(p.Closed) != 1 || p.Dir() != other {
		t.Fatalf("project other: sessions=%v closed=%d dir=%q", p.Sessions, len(p.Closed), p.Dir())
	}
	if p.All.CostUSD == 0 || p.All.CostUSD != projectNamed(t, open, "other").All.CostUSD {
		t.Errorf("project other cost $%.6f, was $%.6f", p.All.CostUSD, projectNamed(t, open, "other").All.CostUSD)
	}
	if closed.Counts.Projects != open.Counts.Projects || closed.All.CostUSD != open.All.CostUSD {
		t.Errorf("projects %d -> %d, total $%.6f -> $%.6f", open.Counts.Projects, closed.Counts.Projects, open.All.CostUSD, closed.All.CostUSD)
	}
	// Every project has a list, even an empty one: the page reads it as such.
	for _, q := range closed.Projects {
		if q.Closed == nil {
			t.Errorf("project %s has no list of closed sessions", q.Name)
		}
	}
}

// What a closed session spent is not dropped for want of knowing its folder.
func TestClosedSessionWithoutAKnownFolderIsKept(t *testing.T) {
	w := fixture(t)
	open := w.svc.Snapshot()
	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	closed := w.svc.Snapshot()
	if closed.All.CostUSD != open.All.CostUSD {
		t.Fatalf("total $%.6f -> $%.6f", open.All.CostUSD, closed.All.CostUSD)
	}
	p := projectNamed(t, closed, "Không rõ thư mục")
	if len(p.Closed) != 1 || p.Closed[0].ID != "sid-a" || p.Path != "" {
		t.Fatalf("project = %+v", p)
	}
}

// Only today's work is brought back: a session that last worked on another
// day is neither listed nor added, and one whose transcript was not touched
// today is not even read.
func TestOnlySessionsAtWorkTodayAreBroughtBack(t *testing.T) {
	w := fixture(t)
	dir := filepath.Join(w.dir, "projects", "old-project")
	// Written to today (a title was added), but all its responses are yesterday's.
	put(t, filepath.Join(dir, "sid-yesterday.jsonl"),
		cwdLine("2026-11-11T03:00:00Z", filepath.Join(w.dir, "work", "old"))+
			msg("msg_y1", "claude-opus-5-5", "2026-11-11T03:10:00Z", "", 10, 900, 0, 9000)+
			`{"type":"ai-title","aiTitle":"Yesterday's work","sessionId":"sid-yesterday"}`+"\n")
	// Not touched since the day before yesterday.
	stale := filepath.Join(dir, "sid-stale.jsonl")
	putAt(t, stale, msg("msg_s1", "claude-opus-5-5", "2026-11-10T03:10:00Z", "", 10, 900, 0, 9000), fixedNow.Add(-48*time.Hour))
	// Not a transcript at all.
	put(t, filepath.Join(dir, "notes.jsonl.bak"), "x")
	put(t, filepath.Join(dir, "has space.jsonl"), msg("msg_x1", "claude-opus-5-5", "2026-11-12T02:10:00Z", "", 10, 900, 0, 9000))

	base := fixture(t).svc.Snapshot()
	snap := w.svc.Snapshot()
	if snap.Counts.ClosedSessions != 0 || snap.All.CostUSD != base.All.CostUSD || len(snap.Projects) != len(base.Projects) {
		t.Fatalf("closed=%d total=$%.6f (want $%.6f) projects=%d", snap.Counts.ClosedSessions, snap.All.CostUSD, base.All.CostUSD, len(snap.Projects))
	}
	for _, m := range snap.Models {
		for _, bm := range base.Models {
			if m.Model == bm.Model && m.All.CostUSD != bm.All.CostUSD {
				t.Errorf("model %s total moved to $%.6f because of a session of another day", m.Model, m.All.CostUSD)
			}
		}
	}
	if _, read := w.svc.files[stale]; read {
		t.Error("a transcript not written to today was read")
	}
}

// A session that is running is never also counted as closed, wherever its
// transcript was found.
func TestRunningSessionIsNotCountedAsClosedToo(t *testing.T) {
	w := fixture(t)
	first := w.svc.Snapshot()
	// A second copy of the same session's transcript, in another folder.
	raw, err := os.ReadFile(filepath.Join(w.project, "sid-b.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(w.dir, "projects", "elsewhere", "sid-b.jsonl"), string(raw))
	// Make the list of ended sessions be drawn up again.
	registryEntry(t, w.dir, 500, "sid-new", filepath.Join(w.dir, "work", "new"), "idle", "new-1")
	snap := w.svc.Snapshot()
	if snap.Counts.ClosedSessions != 0 || snap.All.CostUSD != first.All.CostUSD {
		t.Fatalf("closed=%d total $%.6f -> $%.6f", snap.Counts.ClosedSessions, first.All.CostUSD, snap.All.CostUSD)
	}
}

// What is kept in memory for a closed session is let go once its day is over.
func TestClosedSessionIsLetGoTheNextDay(t *testing.T) {
	w := fixture(t)
	clock := fixedNow
	svc := New(Options{
		ConfigDir:   w.dir,
		PricingPath: filepath.Join(w.dir, "no-override.json"),
		Alive:       func(pid int, _ string) bool { return pid != 300 },
		Now:         func() time.Time { return clock },
		Location:    ict,
	})
	// The files carry the time of the story, not of the test run.
	_ = filepath.WalkDir(filepath.Join(w.dir, "projects"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chtimes(p, fixedNow, fixedNow)
		}
		return nil
	})
	svc.Snapshot()
	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	if snap := svc.Snapshot(); snap.Counts.ClosedSessions != 1 || len(svc.files) != 7 {
		t.Fatalf("on the day: closed=%d files=%d", snap.Counts.ClosedSessions, len(svc.files))
	}
	// Still there however many times the page refreshes that day.
	for range forgetAfter + 2 {
		svc.Snapshot()
	}
	if len(svc.files) != 7 {
		t.Fatalf("a closed session's files were let go the same day: %d left", len(svc.files))
	}

	clock = fixedNow.Add(24 * time.Hour)
	next := svc.Snapshot()
	if next.Counts.ClosedSessions != 0 {
		t.Fatalf("the next day: %d closed sessions still listed", next.Counts.ClosedSessions)
	}
	for range forgetAfter + 1 {
		svc.Snapshot()
	}
	if len(svc.files) != 1 || len(svc.metas) != 0 {
		t.Fatalf("the next day: still holding %d files and %d metas", len(svc.files), len(svc.metas))
	}
}
