package monitor

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"monitor-agent/internal/history"
	"monitor-agent/internal/scan"
	"monitor-agent/internal/usage"
)

// keeping returns a service on the fixture that keeps a history, and a clock
// the test can move.
func (w world) keeping(t *testing.T) (svc *Service, ledger *history.Ledger, now *time.Time) {
	t.Helper()
	ledger, err := history.Open(filepath.Join(w.dir, "app-data", "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	at := fixedNow
	svc = New(Options{
		ConfigDir:   w.dir,
		PricingPath: filepath.Join(w.dir, "no-override.json"),
		Alive:       func(pid int, _ string) bool { return pid != 300 },
		Now:         func() time.Time { return at },
		Location:    ict,
		History:     ledger,
	})
	return svc, ledger, &at
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestHistoryOfTheBoardsSessionsAddsUpToTheBoard(t *testing.T) {
	w := fixture(t)
	svc, _, _ := w.keeping(t)
	snap := svc.Snapshot()
	h := svc.History(2)

	if !h.Kept || h.Today != "2026-11-12" || h.FirstDay != "2026-11-11" || len(h.Days) != 2 {
		t.Fatalf("history = %+v", h)
	}
	yesterday, today := h.Days[0], h.Days[1]
	if yesterday.Day != "2026-11-11" || today.Day != "2026-11-12" {
		t.Fatalf("days = %s, %s", yesterday.Day, today.Day)
	}
	// Today's bar is the figure the till shows for today, to the last digit.
	if today.Spend.CostUSD != snap.TodaySpend.CostUSD || today.Spend.TotalTokens != snap.TodaySpend.TotalTokens {
		t.Errorf("today = $%.9f / %d tokens, the board says $%.9f / %d",
			today.Spend.CostUSD, today.Spend.TotalTokens, snap.TodaySpend.CostUSD, snap.TodaySpend.TotalTokens)
	}
	if !near(yesterday.Spend.CostUSD+today.Spend.CostUSD, snap.All.CostUSD) {
		t.Errorf("two days = $%.9f, the board's total is $%.9f", yesterday.Spend.CostUSD+today.Spend.CostUSD, snap.All.CostUSD)
	}
	if yesterday.Spend.UnpricedTokens != snap.All.UnpricedTokens || yesterday.Spend.UnpricedTokens == 0 {
		t.Errorf("unpriced tokens = %d, the board says %d", yesterday.Spend.UnpricedTokens, snap.All.UnpricedTokens)
	}
	if today.Sessions != 2 || yesterday.Sessions != 1 {
		t.Errorf("sessions at work: %d today, %d yesterday", today.Sessions, yesterday.Sessions)
	}

	// Both sessions ran in one project; three models were at work today.
	if len(today.Projects) != 1 || today.Projects[0].Name != "app" || today.Projects[0].CostUSD != today.Spend.CostUSD {
		t.Errorf("projects today = %+v", today.Projects)
	}
	if len(today.Models) != 3 {
		t.Fatalf("models today = %+v", today.Models)
	}
	sum := 0.0
	for i, m := range today.Models {
		sum += m.CostUSD
		if i > 0 && m.CostUSD > today.Models[i-1].CostUSD {
			t.Errorf("models are not largest first: %+v", today.Models)
		}
	}
	if !near(sum, today.Spend.CostUSD) {
		t.Errorf("models add up to $%.9f, the day is $%.9f", sum, today.Spend.CostUSD)
	}
	// The model without a price is named and counted, not costed.
	var odd HistoryPart
	for _, m := range yesterday.Models {
		if m.Key == "claude-next-9" {
			odd = m
		}
	}
	if odd.CostUSD != 0 || odd.UnpricedTokens == 0 || odd.Tokens != odd.UnpricedTokens {
		t.Errorf("unpriced model = %+v", odd)
	}
}

func TestHistoryHasADayForEveryDateAndIsBounded(t *testing.T) {
	w := fixture(t)
	svc, _, _ := w.keeping(t)
	svc.Snapshot()
	h := svc.History(7)
	if len(h.Days) != 7 || h.Days[0].Day != "2026-11-06" || h.Days[6].Day != "2026-11-12" {
		t.Fatalf("days = %d, from %s to %s", len(h.Days), h.Days[0].Day, h.Days[len(h.Days)-1].Day)
	}
	for _, d := range h.Days[:5] {
		if d.Spend.TotalTokens != 0 || d.Projects == nil || d.Models == nil {
			t.Errorf("an empty day must be empty lists and zeros: %+v", d)
		}
	}
	if n := len(svc.History(100_000).Days); n != HistoryMaxDays {
		t.Errorf("asked for far too many days, got %d", n)
	}
	if n := len(svc.History(-3).Days); n != 1 {
		t.Errorf("asked for no days, got %d", n)
	}

	// Without a ledger there is no history, and asking is harmless.
	if h := w.svc.History(7); h.Kept || len(h.Days) != 7 || h.Days[6].Spend.TotalTokens != 0 {
		t.Errorf("history without a ledger = %+v", h)
	}
	if err := w.svc.Backfill(context.Background()); err != nil || w.svc.SaveHistory() != nil {
		t.Errorf("Backfill without a ledger: %v", err)
	}
}

// oldSession leaves the transcripts of a session that ran on an earlier day
// and is long closed.
func oldSession(t *testing.T, w world) (dir string) {
	t.Helper()
	cwd := filepath.Join(w.dir, "work", "legacy")
	dir = filepath.Join(w.dir, "projects", scan.Slug(cwd))
	mod := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	putAt(t, filepath.Join(dir, "sid-old.jsonl"),
		cwdLine("2026-11-03T02:00:00Z", cwd)+msg("msg_o1", "claude-sonnet-5-5", "2026-11-03T02:00:05Z", "", 100, 200, 0, 0), mod)
	// Its agent begins with a copy of the response that spawned it.
	putAt(t, filepath.Join(dir, "sid-old", "subagents", "agent-x.jsonl"),
		msg("msg_o1", "claude-sonnet-5-5", "2026-11-03T02:00:05Z", "", 100, 200, 0, 0)+
			msg("msg_ox", "claude-haiku-5-5", "2026-11-03T02:01:00Z", "", 1000, 2000, 0, 0), mod)
	return dir
}

func TestBackfillReadsSessionsTheBoardDoesNotShow(t *testing.T) {
	w := fixture(t)
	dir := oldSession(t, w)
	svc, ledger, _ := w.keeping(t)
	snap := svc.Snapshot()
	if err := svc.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}

	h := svc.History(30)
	var day HistoryDay
	for _, d := range h.Days {
		if d.Day == "2026-11-03" {
			day = d
		}
	}
	// Sonnet 100 in + 200 out, counted once; Haiku 1000 in + 2000 out.
	want := int64(100*2_000+200*10_000) + int64(1000*100+2000*500)
	usd(t, "the old day", day.Spend.CostUSD, want)
	if len(day.Projects) != 1 || day.Projects[0].Name != "legacy" || day.Sessions != 1 || len(day.Models) != 2 {
		t.Errorf("old day = %+v", day)
	}
	if h.FirstDay != "2026-11-03" || h.Scan.Running || h.Scan.Total == 0 || h.Scan.Done != h.Scan.Total {
		t.Errorf("first day %s, scan %+v", h.FirstDay, h.Scan)
	}
	// What the board shows was not touched by the pass over the disk.
	if today := h.Days[len(h.Days)-1]; today.Spend.CostUSD != snap.TodaySpend.CostUSD {
		t.Errorf("today = $%.9f after the backfill, the board says $%.9f", today.Spend.CostUSD, snap.TodaySpend.CostUSD)
	}

	// A second pass reads nothing again, and the result is on disk: a new
	// process finds it there even when Claude Code has deleted the transcript.
	if !ledger.Known("sid-old", signature(scan.TranscriptsSince(w.dir, time.Time{})[indexOf(t, w.dir, "sid-old")], scan.AgentFiles(filepath.Join(dir, "sid-old.jsonl")))) {
		t.Error("the old session would be read again on the next pass")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	reopened, err := history.Open(filepath.Join(w.dir, "app-data", "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	later := New(Options{ConfigDir: w.dir, PricingPath: filepath.Join(w.dir, "no-override.json"),
		Alive: func(int, string) bool { return false }, Now: func() time.Time { return fixedNow }, Location: ict, History: reopened})
	if err := later.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, d := range later.History(30).Days {
		if d.Day == "2026-11-03" {
			usd(t, "the old day after its transcript was deleted", d.Spend.CostUSD, want)
			return
		}
	}
	t.Fatal("the old day is gone")
}

func indexOf(t *testing.T, configDir, sessionID string) int {
	t.Helper()
	for i, tr := range scan.TranscriptsSince(configDir, time.Time{}) {
		if tr.SessionID == sessionID {
			return i
		}
	}
	t.Fatalf("no transcript of %s", sessionID)
	return -1
}

func TestHistoryIsKeptForLongerThanOneReportShows(t *testing.T) {
	w := fixture(t)
	svc, _, _ := w.keeping(t)
	from, today, _ := svc.stretch(historyKeepDays)
	if today != "2026-11-12" || from != "2025-10-09" {
		t.Fatalf("%d days back from %s is %s", historyKeepDays, today, from)
	}
	if historyKeepDays <= HistoryMaxDays {
		t.Fatal("what is kept must reach further back than what one report shows")
	}
}

func TestSessionReadOnlyInPartIsNotTakenForWhole(t *testing.T) {
	w := fixture(t)
	dir := oldSession(t, w)
	main := filepath.Join(dir, "sid-old.jsonl")
	agents := scan.AgentFiles(main)

	rows, cwd, whole, ok := readSession(main, agents, ict)
	if !ok || !whole || len(rows) != 2 || cwd == "" {
		t.Fatalf("a session read whole: rows=%d whole=%v ok=%v", len(rows), whole, ok)
	}
	// One of its agents' transcripts is listed but cannot be opened. What
	// could be read is still worth having; it is not the last word, and
	// Backfill files such a reading without a signature, to be done again.
	gone := append(agents, scan.AgentFile{ID: "y", Path: filepath.Join(dir, "sid-old", "subagents", "agent-y.jsonl")})
	rows, _, whole, ok = readSession(main, gone, ict)
	if !ok || whole || len(rows) != 2 {
		t.Fatalf("a session read in part: rows=%d whole=%v ok=%v", len(rows), whole, ok)
	}
	// With no main transcript there is no session to speak of.
	if _, _, _, ok := readSession(filepath.Join(dir, "absent.jsonl"), nil, ict); ok {
		t.Fatal("a session without a transcript was read")
	}
}

func TestSessionThatClosedWithoutWorkTodayStillReachesTheHistory(t *testing.T) {
	w := fixture(t)
	// A session whose last lines are yesterday's, but whose file was touched
	// today: it ended around midnight. It is not listed, and still counted.
	cwd := filepath.Join(w.dir, "work", "late")
	putAt(t, filepath.Join(w.dir, "projects", scan.Slug(cwd), "sid-late.jsonl"),
		cwdLine("2026-11-11T16:59:00Z", cwd)+msg("msg_l1", "claude-sonnet-5-5", "2026-11-11T16:59:30Z", "", 100, 200, 0, 0), fixedNow)
	svc, ledger, _ := w.keeping(t)
	snap := svc.Snapshot()
	for _, p := range snap.Projects {
		if p.Name == "late" {
			t.Fatal("a session that did nothing today is listed as closed today")
		}
	}
	found := false
	ledger.Each(func(id string, e history.Entry) { found = found || id == "sid-late" })
	if !found {
		t.Fatal("what it consumed yesterday never reached the history")
	}
}

func TestAwaitBackfillSaysTheHistoryIsOnItsWay(t *testing.T) {
	w := fixture(t)
	svc, _, _ := w.keeping(t)
	svc.AwaitBackfill()
	if !svc.History(7).Scan.Running {
		t.Fatal("a page asking before the first pass is told nothing is being read")
	}
	if err := svc.Backfill(context.Background()); err != nil || svc.History(7).Scan.Running {
		t.Fatalf("after the pass: err=%v, still marked as running", err)
	}
	// Without a ledger there is nothing to wait for.
	w.svc.AwaitBackfill()
	if w.svc.History(7).Scan.Running {
		t.Fatal("a service that keeps no history says it is reading one")
	}
}

func TestBackfillStopsWhenAsked(t *testing.T) {
	w := fixture(t)
	oldSession(t, w)
	svc, _, _ := w.keeping(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Backfill(ctx); err != nil {
		t.Fatal(err)
	}
	if h := svc.History(30); h.FirstDay != "" || h.Scan.Running {
		t.Fatalf("a cancelled pass read something or is still marked as running: %+v", h.Scan)
	}
}

func TestHistoryRowsAndTheirExport(t *testing.T) {
	w := fixture(t)
	svc, _, _ := w.keeping(t)
	snap := svc.Snapshot()
	rows := svc.HistoryRows(2)
	if len(rows) != 5 {
		t.Fatalf("rows = %+v", rows)
	}
	sum := 0.0
	unpriced := 0
	for i, r := range rows {
		sum += r.CostUSD
		if !r.Priced {
			unpriced++
		}
		if i > 0 && r.Day < rows[i-1].Day {
			t.Errorf("rows are not oldest first")
		}
		if r.Project != "app" {
			t.Errorf("row %d project = %q", i, r.Project)
		}
	}
	if !near(sum, snap.All.CostUSD) || unpriced != 1 {
		t.Errorf("rows add up to $%.9f with %d unpriced; the board says $%.9f", sum, unpriced, snap.All.CostUSD)
	}

	var buf bytes.Buffer
	evil := []HistoryRow{
		{Day: "2026-11-12", Project: "=HYPERLINK(\"http://x\")", Path: `C:\work\=x`, Model: "claude-opus-5-5",
			Tokens: usage.Tokens{Input: 10, Output: 20, CacheRead: 30, Requests: 2}, CostUSD: 1.5, Priced: true},
		{Day: "2026-11-12", Project: "+app", Path: "@home", Model: "claude-next-9", Fast: true, Tokens: usage.Tokens{Input: 7}},
	}
	if err := WriteHistoryCSV(&buf, evil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\uFEFFNgày,Project,") {
		t.Errorf("no byte-order mark or heading: %q", out[:min(len(out), 40)])
	}
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\r\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	// A cell that would be read as a formula is made text; a path that only
	// contains such a character is left as it is.
	if lines[1] != `2026-11-12,"'=HYPERLINK(""http://x"")",C:\work\=x,claude-opus-5-5,,10,20,30,0,0,0,2,60,1.500000` {
		t.Errorf("line 1 = %s", lines[1])
	}
	if lines[2] != `2026-11-12,'+app,'@home,claude-next-9,fast,7,0,0,0,0,0,0,7,` {
		t.Errorf("line 2 = %s", lines[2])
	}
}

func TestUsageIsWorkedOutAgainOnlyWhenATranscriptGrows(t *testing.T) {
	w := fixture(t)
	main := filepath.Join(w.project, "sid-a.jsonl")
	first := w.svc.Snapshot()
	kept := w.svc.usages[main]
	if kept == nil || len(kept.agents) != 5 {
		t.Fatalf("usage of sid-a after the first snapshot: %+v", kept)
	}
	second := w.svc.Snapshot()
	if w.svc.usages[main] != kept {
		t.Fatal("nothing changed, and the session was added up again")
	}
	if second.All.CostUSD != first.All.CostUSD || sessionA(t, second).All.TotalTokens != sessionA(t, first).All.TotalTokens {
		t.Fatalf("the kept figures differ: $%.9f then $%.9f", first.All.CostUSD, second.All.CostUSD)
	}

	// One more response from one agent: 1 in + 10 out + 1000 cache read, Opus 5.5.
	appendTo(t, filepath.Join(w.sub, "agent-run.jsonl"), msg("msg_run-3", "claude-opus-5-5", "2026-11-12T02:59:50Z", "", 1, 10, 0, 1000))
	third := w.svc.Snapshot()
	if w.svc.usages[main] == kept {
		t.Fatal("a transcript grew and the old figures were kept")
	}
	usd(t, "what the new response added", third.All.CostUSD-first.All.CostUSD, 1*4_000+10*20_000+1000*200)

	// A session that is gone takes its kept figures with it.
	for range forgetAfter + 2 {
		if err := os.Remove(filepath.Join(w.dir, "sessions", "200.json")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		w.svc.Snapshot()
	}
	// sid-b closed today, so it is still on the page and still kept.
	if w.svc.usages[filepath.Join(w.project, "sid-b.jsonl")] == nil {
		t.Error("a session that closed today lost its kept figures while still shown")
	}
}

func TestAgentOfASessionAtRestIsFoundOnceTheListAges(t *testing.T) {
	w := fixture(t)
	svc, _, now := w.keeping(t)
	sessionB := func(snap Snapshot) Session {
		for _, p := range snap.Projects {
			for _, s := range p.Sessions {
				if s.ID == "sid-b" {
					return s
				}
			}
		}
		t.Fatal("session sid-b not in the snapshot")
		return Session{}
	}
	if n := len(sessionB(svc.Snapshot()).Agents); n != 0 {
		t.Fatalf("sid-b has %d agents", n)
	}
	// sid-b is idle and nothing of it is at work, so its folders are not
	// walked on every refresh.
	agentFiles(t, filepath.Join(w.project, "sid-b", "subagents"), "late", "claude-opus-5-5", "2026-11-12T02:59:00Z", "2026-11-12T02:59:30Z", "")
	if n := len(sessionB(svc.Snapshot()).Agents); n != 0 {
		t.Fatalf("the list was walked again at once: %d agents", n)
	}
	*now = now.Add(listEvery)
	b := sessionB(svc.Snapshot())
	if len(b.Agents) != 1 || b.Agents[0].ID != "late" {
		t.Fatalf("agents after the list aged: %+v", b.Agents)
	}
	// Now one is at work, and the session is looked at every time.
	agentFiles(t, filepath.Join(w.project, "sid-b", "subagents"), "later", "claude-opus-5-5", "2026-11-12T02:59:00Z", "2026-11-12T02:59:30Z", "")
	if n := len(sessionB(svc.Snapshot()).Agents); n != 2 {
		t.Fatalf("with an agent at work: %d agents", n)
	}
}

func TestPulseIsSentOnlyForWhatIsStillWorking(t *testing.T) {
	w := fixture(t)
	snap := w.svc.Snapshot()
	a := sessionA(t, snap)
	if len(a.Pulse) != PulseMinutes {
		t.Errorf("busy session: pulse of %d", len(a.Pulse))
	}
	for _, ag := range a.Agents {
		working := ag.Status == StatusRunning || ag.Status == StatusQuiet
		if ag.Pulse == nil || (len(ag.Pulse) == PulseMinutes) != working {
			t.Errorf("agent %s (%s): pulse of %d", ag.ID, ag.Status, len(ag.Pulse))
		}
	}
	for _, p := range snap.Projects {
		for _, s := range p.Sessions {
			if s.Status != "busy" && (s.Pulse == nil || len(s.Pulse) != 0) {
				t.Errorf("session %s (%s): pulse %v; it must be an empty list for the page", s.ID, s.Status, s.Pulse)
			}
		}
	}
}

func TestClosedSessionCanBeLookedIntoAndOpenedAgain(t *testing.T) {
	w := fixture(t)
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"), cwdLine("2026-11-12T02:59:40Z", w.cwd))
	w.svc.Snapshot()
	if _, err := w.svc.Resumable("sid-a"); err == nil {
		t.Fatal("a running session was offered for opening again")
	}
	if tr, err := w.svc.Trail("sid-a", "", false); err != nil || tr.Closed {
		t.Fatalf("trail of the running session: closed=%v err=%v", tr.Closed, err)
	}

	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	w.svc.Snapshot()

	tr, err := w.svc.Trail("sid-a", "", false)
	if err != nil || !tr.Closed || len(tr.Steps) != 3 {
		t.Fatalf("trail of the closed session: %+v, err=%v", tr, err)
	}
	// Asked again with nothing new in the transcript, the answer is the same.
	if again, err := w.svc.Trail("sid-a", "", false); err != nil || len(again.Steps) != len(tr.Steps) || again.Steps[1] != tr.Steps[1] {
		t.Fatalf("second reading differs: %+v, err=%v", again, err)
	}
	// With the contents hidden it is not the kept answer that comes back.
	if hidden, err := w.svc.Trail("sid-a", "", true); err != nil || !hidden.Hidden || hidden.Steps[1].Detail != "" {
		t.Fatalf("hidden trail after a shown one: %+v, err=%v", hidden, err)
	}
	if ag, err := w.svc.Trail("sid-a", "run", false); err != nil || !ag.Closed || len(ag.Steps) != 2 {
		t.Fatalf("trail of the closed session's agent: %+v, err=%v", ag, err)
	}

	dir, err := w.svc.Resumable("sid-a")
	if err != nil || dir != w.cwd {
		t.Fatalf("Resumable = %q, %v", dir, err)
	}
	for _, id := range []string{"sid-b", "sid-nope", "", `..\sid-a`} {
		if _, err := w.svc.Resumable(id); err == nil {
			t.Errorf("Resumable(%q) succeeded", id)
		}
	}
}

func TestSessionFiledUnderAnotherSpellingOfItsFolderIsOpenedAgain(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("capitals tell folders apart here")
	}
	w := fixture(t)
	// The same folder, its drive letter (or any letter) in the other case.
	other := strings.ToUpper(w.cwd)
	if other == w.cwd {
		other = strings.ToLower(w.cwd)
	}
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"), cwdLine("2026-11-12T02:59:40Z", other))
	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	w.svc.Snapshot()
	if dir, err := w.svc.Resumable("sid-a"); err != nil || dir != other {
		t.Fatalf("Resumable = %q, %v", dir, err)
	}
}

func TestSessionThatMovedFoldersIsNotOpenedAgain(t *testing.T) {
	w := fixture(t)
	// Its last lines say it ran somewhere else than where its transcript is
	// filed. Claude Code would look for it under the wrong folder.
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"), cwdLine("2026-11-12T02:59:40Z", filepath.Join(w.dir, "work", "elsewhere")))
	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	w.svc.Snapshot()
	if dir, err := w.svc.Resumable("sid-a"); err == nil {
		t.Fatalf("Resumable = %q", dir)
	}
}

func TestModelChoicesAreTheNewestVerifiedOfEachFamily(t *testing.T) {
	w := fixture(t)
	got := w.svc.ModelChoices()
	want := []struct {
		id, label string
		nano      int64
	}{
		{"claude-fable-5-2", "Fable 5.2", 480_000_000},
		{"claude-opus-5-5", "Opus 5.5", 240_000_000},
		{"claude-sonnet-5-5", "Sonnet 5.5", 120_000_000},
		{"claude-haiku-5-5", "Haiku 5.5", 6_000_000},
	}
	if len(got) != len(want) {
		t.Fatalf("choices = %+v", got)
	}
	for i, c := range want {
		if got[i].ID != c.id || got[i].Label != c.label {
			t.Errorf("choice %d = %+v, want %s", i, got[i], c.id)
		}
		usd(t, "opening a session with "+c.label, got[i].OpenUSD, c.nano)
	}
}

func TestFamily(t *testing.T) {
	for id, want := range map[string]string{
		"claude-opus-5-5":   "opus 5.5",
		"claude-fable-5":    "fable 5",
		"claude-3-5-sonnet": "",
		"claude-opus":       "",
		"gpt-4":             "gpt 4",
		"claude--5":         "",
	} {
		name, version, ok := family(id)
		got := ""
		if ok {
			parts := make([]string, len(version))
			for i, n := range version {
				parts[i] = strconv.Itoa(n)
			}
			got = name + " " + strings.Join(parts, ".")
		}
		if got != want {
			t.Errorf("family(%q) = %q, want %q", id, got, want)
		}
	}
	if !newer([]int{5, 5}, []int{5, 2}) || newer([]int{4, 8}, []int{5}) || !newer([]int{5, 1}, []int{5}) || newer([]int{5}, []int{5}) {
		t.Error("newer orders versions wrongly")
	}
}
