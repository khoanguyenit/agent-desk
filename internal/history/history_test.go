package history

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"monitor-agent/internal/usage"
)

func row(day, model string, in int64) Row {
	return Row{Day: day, Model: model, Tokens: usage.Tokens{Input: in, Requests: 1}}
}

func entries(l *Ledger) map[string]Entry {
	out := map[string]Entry{}
	l.Each(func(id string, e Entry) { out[id] = e })
	return out
}

func TestRecordKeepsRowsInOrderAndDropsWhatCannotBeUsed(t *testing.T) {
	l, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	l.Record("s1", `C:\work\app`, []Row{
		row("2026-11-12", "claude-opus-5-5", 5),
		row("2026-11-11", "claude-opus-5-5", 1),
		row("2026-11-12", "claude-opus-5-5", 7), // same day and model: one row
		{Day: "2026-11-12", Model: "claude-opus-5-5", Fast: true, Tokens: usage.Tokens{Output: 3}},
		row("", "claude-opus-5-5", 9),                                                        // no day
		row("12/11/2026", "claude-opus-5-5", 9),                                              // not a date in the kept shape
		row("2026-11-12", "", 9),                                                             // no model
		{Day: "2026-11-12", Model: "claude-x-1"},                                             // counts nothing
		{Day: "2026-11-10", Model: "claude-x-1", Tokens: usage.Tokens{Input: -4, Output: 2}}, // a negative count is no count
	})
	got := entries(l)["s1"]
	want := []Row{
		{Day: "2026-11-10", Model: "claude-x-1", Tokens: usage.Tokens{Output: 2}},
		row("2026-11-11", "claude-opus-5-5", 1),
		{Day: "2026-11-12", Model: "claude-opus-5-5", Tokens: usage.Tokens{Input: 12, Requests: 2}},
		{Day: "2026-11-12", Model: "claude-opus-5-5", Fast: true, Tokens: usage.Tokens{Output: 3}},
	}
	if got.Cwd != `C:\work\app` || !reflect.DeepEqual(got.Rows, want) {
		t.Fatalf("entry = %+v\n want rows %+v", got, want)
	}
}

func TestLedgerSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "history.json")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a ledger nothing was put into was written to disk")
	}

	l.Record("live", "/work/app", []Row{row("2026-11-12", "claude-opus-5-5", 5)})
	l.Fill("old", "/work/legacy", "sig-1", []Row{row("2026-11-03", "claude-sonnet-5-5", 8)})
	l.Fill("empty", "/work/legacy", "sig-2", nil)
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entries(again), entries(l); !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopening: %+v\n want %+v", got, want)
	}
	// What was read from disk and has not changed needs no second reading;
	// the session the board had does, because the board is gone with the
	// process that ran it.
	if !again.Known("old", "sig-1") || !again.Known("empty", "sig-2") || again.Known("old", "sig-other") || again.Known("live", "") {
		t.Fatal("Known does not tell what needs reading again")
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "\n  ") {
		t.Error("the file is written compact; it is not meant to be edited")
	}
}

func TestBoardsReadingOutranksOnePassOverTheDisk(t *testing.T) {
	l, _ := Open("")
	l.Record("s1", "/work/app", []Row{row("2026-11-12", "claude-opus-5-5", 50)})
	if l.Fill("s1", "/work/app", "sig", []Row{row("2026-11-12", "claude-opus-5-5", 10)}) {
		t.Fatal("Fill replaced what the board recorded")
	}
	if got := entries(l)["s1"].Rows[0].Tokens.Input; got != 50 {
		t.Fatalf("input = %d, want the board's 50", got)
	}
	if !l.Known("s1", "anything") {
		t.Fatal("a session the board has must not be read from disk")
	}
	// A session the board records before it has anything dated keeps what
	// was known of it.
	l.Fill("s2", "/work/app", "sig", []Row{row("2026-11-01", "claude-opus-5-5", 3)})
	l.Record("s2", "/work/app", nil)
	if got := entries(l)["s2"]; len(got.Rows) != 1 {
		t.Fatalf("an empty reading wiped the entry: %+v", got)
	}
}

func TestPruneDropsWhatIsTooOld(t *testing.T) {
	l, _ := Open("")
	l.Record("mixed", "/a", []Row{row("2025-01-01", "m", 1), row("2026-11-12", "m", 2)})
	l.Record("old", "/a", []Row{row("2025-01-01", "m", 1)})
	l.Fill("old-read", "/a", "sig", []Row{row("2025-01-01", "m", 1)})
	l.Prune("2026-01-01")
	got := entries(l)
	if len(got["mixed"].Rows) != 1 || got["mixed"].Rows[0].Day != "2026-11-12" {
		t.Errorf("mixed = %+v", got["mixed"])
	}
	if _, kept := got["old"]; kept {
		t.Error("an entry with nothing left was kept")
	}
	// Its files are known, so it stays, empty, and is not read again.
	if e, kept := got["old-read"]; !kept || len(e.Rows) != 0 || !l.Known("old-read", "sig") {
		t.Errorf("old-read = %+v, kept=%v", e, kept)
	}
}

func TestFileThatCannotBeUnderstoodIsPutAsideNotWrittenOver(t *testing.T) {
	const body = "{{{{"
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Twice over: the second file put aside must not write over the first.
	for round, want := range []string{path + ".bak", ""} {
		if round == 1 {
			if err := os.WriteFile(path, []byte(body+"again"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		l, err := Open(path)
		if err == nil || !l.Damaged() || l.Frozen() || len(entries(l)) != 0 {
			t.Fatalf("round %d: err=%v damaged=%v frozen=%v entries=%d", round, err, l.Damaged(), l.Frozen(), len(entries(l)))
		}
		l.Record("s1", "/a", []Row{row("2026-11-12", "m", int64(round)+1)})
		if err := l.Save(); err != nil {
			t.Fatal(err)
		}
		if again, err := Open(path); err != nil || again.Damaged() || len(entries(again)) != 1 {
			t.Fatalf("round %d: the new file: err=%v", round, err)
		}
		if want != "" {
			if kept, _ := os.ReadFile(want); string(kept) != body {
				t.Fatalf("the old file was not kept: %q", kept)
			}
		}
	}
	first, _ := os.ReadFile(path + ".bak")
	others, _ := filepath.Glob(path + ".*.bak")
	if string(first) != body || len(others) != 1 {
		t.Fatalf("after two rounds: first aside = %q, %d others", first, len(others))
	}
	if second, _ := os.ReadFile(others[0]); string(second) != body+"again" {
		t.Fatalf("second file put aside = %q", second)
	}
	if left, _ := filepath.Glob(path + ".*.tmp"); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

// What the file holds cannot be had again once the transcripts are gone. A
// file that could not be read, or that a newer build wrote, is left alone.
func TestFileThatCannotBeReadIsLeftAlone(t *testing.T) {
	newer := `{"version":99,"sessions":{"s":{"cwd":"/a","rows":[{"day":"2026-11-12","model":"m","tokens":{"input":1}}]}}}`
	dir := t.TempDir()
	fromNewer := filepath.Join(dir, "newer.json")
	if err := os.WriteFile(fromNewer, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	notAFile := filepath.Join(dir, "folder.json")
	if err := os.Mkdir(notAFile, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"from a newer build": fromNewer, "not a file": notAFile} {
		l, err := Open(path)
		if err == nil || !l.Frozen() || l.Damaged() {
			t.Fatalf("%s: err=%v frozen=%v damaged=%v", name, err, l.Frozen(), l.Damaged())
		}
		// The run goes on in memory, and nothing reaches the disk.
		l.Record("s1", "/a", []Row{row("2026-11-12", "m", 1)})
		if err := l.Save(); err != nil || len(entries(l)) != 1 {
			t.Fatalf("%s: Save err=%v, entries=%d", name, err, len(entries(l)))
		}
		if _, err := os.Stat(path + ".bak"); err == nil {
			t.Errorf("%s: the file was put aside", name)
		}
	}
	if raw, _ := os.ReadFile(fromNewer); string(raw) != newer {
		t.Fatalf("the newer build's file was changed: %q", raw)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestSaveWritesOnlyWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	l, _ := Open(path)
	l.Record("s1", "/a", []Row{row("2026-11-12", "m", 1)})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Saying the same thing again is not a change.
	l.Record("s1", "/a", []Row{row("2026-11-12", "m", 1)})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("an unchanged ledger was written again")
	}
	l.Record("s1", "/a", []Row{row("2026-11-12", "m", 2)})
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	if again, err := os.Stat(path); err != nil || again.Size() != first.Size() {
		t.Fatalf("after a change: %v", err)
	}
}

func TestCountsAreBounded(t *testing.T) {
	l, _ := Open("")
	huge := usage.Tokens{Input: 1 << 62, Output: 1 << 62}
	l.Record("s1", "/a", []Row{
		{Day: "2026-11-12", Model: "m", Tokens: huge},
		{Day: "2026-11-12", Model: "m", Tokens: huge},
	})
	got := entries(l)["s1"].Rows
	if len(got) != 1 || got[0].Tokens.Input != maxCount || got[0].Tokens.Output != maxCount || got[0].Tokens.Total() <= 0 {
		t.Fatalf("rows = %+v; a count must stay within bounds, merged or not", got)
	}
}

func TestReadingThatMayBeIncompleteIsDoneAgain(t *testing.T) {
	l, _ := Open("")
	// Read in part: kept, but the session is not known yet.
	l.Fill("s1", "/a", "", []Row{row("2026-11-03", "m", 4)})
	if len(entries(l)["s1"].Rows) != 1 || l.Known("s1", "") || l.Known("s1", "sig") {
		t.Fatal("a partial reading must be kept and read again")
	}
	// Nothing read at all, and no word on the files: nothing to remember.
	l.Fill("s2", "/a", "", nil)
	if _, kept := entries(l)["s2"]; kept {
		t.Fatal("an empty partial reading was stored")
	}
}

func TestForgetEmptyDropsWhatHasNoRowsAndNoFiles(t *testing.T) {
	l, _ := Open("")
	l.Fill("gone", "/a", "sig", nil)
	l.Fill("there", "/a", "sig", nil)
	l.Fill("gone-with-rows", "/a", "sig", []Row{row("2026-11-03", "m", 1)})
	l.Record("board", "/a", nil)
	l.ForgetEmpty(func(id string) bool { return id == "there" })
	got := entries(l)
	if _, kept := got["gone"]; kept {
		t.Error("an empty entry whose transcript is gone was kept")
	}
	if _, kept := got["there"]; !kept {
		t.Error("an empty entry whose transcript is still on disk was dropped")
	}
	if _, kept := got["gone-with-rows"]; !kept {
		t.Error("history was dropped because its transcript is gone, which is what it is kept for")
	}
}

func TestValidDay(t *testing.T) {
	for s, want := range map[string]bool{
		"2026-11-12": true, "2026-1-12": false, "2026/11/12": false, "": false, "2026-11-1x": false, "20261112": false,
	} {
		if ValidDay(s) != want {
			t.Errorf("ValidDay(%q) = %v", s, !want)
		}
	}
}
