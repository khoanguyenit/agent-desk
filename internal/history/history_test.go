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

func TestFileThatCannotBeUsedIsPutAsideNotWrittenOver(t *testing.T) {
	for name, body := range map[string]string{
		"not json":           "{{{{",
		"from a newer build": `{"version":99,"sessions":{"s":{"cwd":"/a","rows":[{"day":"2026-11-12","model":"m","tokens":{"input":1}}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			l, err := Open(path)
			if err == nil || !l.Damaged() || len(entries(l)) != 0 {
				t.Fatalf("err=%v damaged=%v entries=%d", err, l.Damaged(), len(entries(l)))
			}
			l.Record("s1", "/a", []Row{row("2026-11-12", "m", 1)})
			if err := l.Save(); err != nil {
				t.Fatal(err)
			}
			if kept, _ := os.ReadFile(path + ".bak"); string(kept) != body {
				t.Fatalf("the old file was not kept: %q", kept)
			}
			if again, err := Open(path); err != nil || again.Damaged() || len(entries(again)) != 1 {
				t.Fatalf("the new file: err=%v", err)
			}
		})
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
