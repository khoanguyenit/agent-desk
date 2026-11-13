package scan

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSlug(t *testing.T) {
	vietnamese := "d:\\d" + string(rune(0x1EF1)) + " " + string(rune(0xE1)) + "n" // "d:\dự án"
	emoji := "/tmp/" + string(rune(0x1F600)) + "x"
	cases := map[string]string{
		`d:\work\Big Bank - Core API`: "d--work-Big-Bank---Core-API",
		`d:\work\side\monitor-agent`:  "d--work-side-monitor-agent",
		`C:\Users\dev.one`:            "C--Users-dev-one",
		"/home/dev/my_app":            "-home-dev-my-app",
		vietnamese:                    "d--d---n",
		// Outside the Basic Multilingual Plane: two UTF-16 units, two dashes.
		emoji: "-tmp---x",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLiveSessionsKeepsOnlyRunningProcesses(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions", "200.json"),
		`{"pid":200,"sessionId":"bbb","cwd":"d:\\b","startedAt":2000,"procStart":"222","status":"idle","name":"second","updatedAt":2500}`)
	write(t, filepath.Join(dir, "sessions", "100.json"),
		`{"pid":100,"sessionId":"aaa","cwd":"d:\\a","startedAt":1000,"procStart":"111","status":"busy","name":"first","kind":"interactive","entrypoint":"claude-vscode","version":"2.1.322"}`)
	write(t, filepath.Join(dir, "sessions", "300.json"),
		`{"pid":300,"sessionId":"ccc","cwd":"d:\\c","startedAt":3000,"procStart":"333","status":"busy"}`)
	// procStart written as a number, as it may be on other platforms.
	write(t, filepath.Join(dir, "sessions", "400.json"),
		`{"pid":400,"sessionId":"ddd","cwd":"/d","startedAt":4000,"procStart":98765}`)
	write(t, filepath.Join(dir, "sessions", "500.json"), `{broken`)
	write(t, filepath.Join(dir, "sessions", "600.json"), `{"pid":600,"cwd":"d:\\no-session-id"}`)
	write(t, filepath.Join(dir, "sessions", "100.abc.key"), `secret`)
	if err := os.MkdirAll(filepath.Join(dir, "sessions", "700.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	seen := map[int]string{}
	alive := func(pid int, procStart string) bool {
		seen[pid] = procStart
		return pid != 300 // 300 has exited
	}
	got, err := LiveSessions(dir, alive)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3: %+v", len(got), got)
	}
	if got[0].PID != 100 || got[1].PID != 200 || got[2].PID != 400 {
		t.Fatalf("order = %d,%d,%d, want oldest first", got[0].PID, got[1].PID, got[2].PID)
	}
	first := got[0]
	if first.SessionID != "aaa" || first.Cwd != `d:\a` || first.Status != "busy" || first.Name != "first" ||
		first.Entrypoint != "claude-vscode" || first.Version != "2.1.322" || first.StartedAt.UnixMilli() != 1000 {
		t.Fatalf("first session = %+v", first)
	}
	if seen[100] != "111" || seen[400] != "98765" {
		t.Fatalf("start markers passed to the liveness check = %v", seen)
	}
	// When its status last changed: updatedAt here, which entries without
	// statusUpdatedAt move along with the status; nothing when neither is set.
	if got[1].StatusSince.UnixMilli() != 2500 || !first.StatusSince.IsZero() {
		t.Fatalf("status since = %v, %v", got[1].StatusSince, first.StatusSince)
	}
	if _, checked := seen[600]; checked {
		t.Fatal("an entry without a session id reached the liveness check")
	}
}

func TestStatusSincePrefersStatusUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sessions", "100.json"),
		`{"pid":100,"sessionId":"aaa","cwd":"d:\\a","procStart":"111","status":"waiting","updatedAt":9000,"statusUpdatedAt":7000}`)
	got, err := LiveSessions(dir, func(int, string) bool { return true })
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0].StatusSince.UnixMilli() != 7000 {
		t.Fatalf("status since = %v, want the time the status was written", got[0].StatusSince)
	}
}

func TestLiveSessionsWithoutRegistry(t *testing.T) {
	got, err := LiveSessions(t.TempDir(), func(int, string) bool { return true })
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; a machine without Claude Code sessions is not an error", got, err)
	}
}

// Claude Code rewrites a registry entry when a session's status changes. A
// read that lands mid-write must not make the session disappear.
func TestRegistryRemembersAnEntryThatIsBrieflyUnreadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions", "100.json")
	entry := `{"pid":100,"sessionId":"aaa","cwd":"d:\\a","startedAt":1000,"procStart":"1","status":"busy"}`
	write(t, path, entry)

	var reg Registry
	alive := func(int, string) bool { return true }
	live := func() []Session {
		t.Helper()
		got, err := reg.Live(dir, alive)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := live(); len(got) != 1 || got[0].Status != "busy" {
		t.Fatalf("first read: %+v", got)
	}

	// Mid-rewrite: the file exists but is empty.
	write(t, path, "")
	if got := live(); len(got) != 1 || got[0].Status != "busy" {
		t.Fatalf("while unreadable: %+v, want the last good reading", got)
	}

	// The rewrite completes with a new status.
	write(t, path, `{"pid":100,"sessionId":"aaa","cwd":"d:\\a","startedAt":1000,"procStart":"1","status":"idle"}`)
	if got := live(); len(got) != 1 || got[0].Status != "idle" {
		t.Fatalf("after the rewrite: %+v", got)
	}

	// The session ends and its entry is removed: the memory must not keep it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := live(); len(got) != 0 {
		t.Fatalf("after removal: %+v, want none", got)
	}

	// A stateless read never invents an entry from a broken file.
	write(t, path, "")
	if got, _ := LiveSessions(dir, alive); len(got) != 0 {
		t.Fatalf("stateless read of a broken file: %+v", got)
	}
}

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid(), "") {
		t.Fatal("this test's own process is reported as not running")
	}
	for _, pid := range []int{0, -1, math.MaxInt32 + 1, math.MaxInt64} {
		if ProcessAlive(pid, "") {
			t.Errorf("pid %d reported as running", pid)
		}
	}
}

func TestTranscriptPath(t *testing.T) {
	dir := t.TempDir()
	direct := filepath.Join(dir, "projects", "d--work-app", "sid-1.jsonl")
	moved := filepath.Join(dir, "projects", "some-other-slug", "sid-2.jsonl")
	write(t, direct, "{}\n")
	write(t, moved, "{}\n")

	if got, ok := TranscriptPath(dir, Session{SessionID: "sid-1", Cwd: `d:\work\app`}); !ok || got != direct {
		t.Errorf("direct lookup = %q, %v", got, ok)
	}
	if got, ok := TranscriptPath(dir, Session{SessionID: "sid-2", Cwd: `d:\work\app`}); !ok || got != moved {
		t.Errorf("fallback lookup = %q, %v", got, ok)
	}
	if _, ok := TranscriptPath(dir, Session{SessionID: "sid-3", Cwd: `d:\work\app`}); ok {
		t.Error("found a transcript that does not exist")
	}
	for _, bad := range []string{"", `..\..\secret`, "../x", "a/b", "sid 1"} {
		if _, ok := TranscriptPath(dir, Session{SessionID: bad, Cwd: `d:\work\app`}); ok {
			t.Errorf("session id %q must be rejected", bad)
		}
	}
}

func TestAgentFilesAndMeta(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "projects", "p", "sid.jsonl")
	sub := filepath.Join(dir, "projects", "p", "sid", "subagents")
	write(t, transcript, "{}\n")
	write(t, filepath.Join(sub, "agent-a1.jsonl"), "{}\n")
	write(t, filepath.Join(sub, "agent-a1.meta.json"),
		`{"agentType":"go-reviewer","description":"Go review","requestShape":"background","spawnDepth":1,"pickedEffortModel":"claude-opus-5-5","finished":true}`)
	write(t, filepath.Join(sub, "agent-b2.jsonl"), "{}\n")
	write(t, filepath.Join(sub, "workflows", "wf_abc-123", "agent-c3.jsonl"), "{}\n")
	write(t, filepath.Join(sub, "workflows", "wf_abc-123", "agent-c3.meta.json"),
		`{"agentType":"workflow-subagent","description":"read:store","workflowPhase":"Understand","requestShape":"foreground"}`)
	write(t, filepath.Join(sub, "notes.txt"), "ignored")
	write(t, filepath.Join(dir, "projects", "p", "sid", "tool-results", "agent-zz.jsonl"), "{}\n")
	// A directory that happens to be named like a transcript is not one.
	if err := os.MkdirAll(filepath.Join(sub, "agent-dir.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}

	files := AgentFiles(transcript)
	if len(files) != 3 {
		t.Fatalf("got %d agent files, want 3: %+v", len(files), files)
	}
	byID := map[string]AgentFile{}
	for _, f := range files {
		byID[f.ID] = f
	}
	if byID["a1"].WorkflowID != "" || byID["c3"].WorkflowID != "wf_abc-123" {
		t.Fatalf("workflow ids = %q, %q", byID["a1"].WorkflowID, byID["c3"].WorkflowID)
	}

	m, err := ReadMeta(byID["a1"].MetaPath)
	if err != nil || m.AgentType != "go-reviewer" || !m.Finished || m.RequestShape != "background" || m.PickedModel != "claude-opus-5-5" {
		t.Fatalf("meta a1 = %+v, %v", m, err)
	}
	m, err = ReadMeta(byID["c3"].MetaPath)
	if err != nil || m.WorkflowPhase != "Understand" || m.Finished {
		t.Fatalf("meta c3 = %+v, %v", m, err)
	}
	// An agent that has no meta file yet is still listed, with an empty meta.
	m, err = ReadMeta(byID["b2"].MetaPath)
	if err != nil || m != (AgentMeta{}) {
		t.Fatalf("meta b2 = %+v, %v", m, err)
	}

	if got := AgentFiles(filepath.Join(dir, "projects", "p", "no-such-session.jsonl")); len(got) != 0 {
		t.Fatalf("a session without subagents returned %+v", got)
	}
}
