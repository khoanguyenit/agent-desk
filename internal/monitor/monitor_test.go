package monitor

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"monitor-agent/internal/scan"
)

var ict = time.FixedZone("ICT", 7*3600)

// The fixture's "now": 10:00 on 12 Nov 2026 in UTC+7, which is 03:00 UTC.
var fixedNow = time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// putAt writes a file and gives it a modification time from the fixture's
// clock, so nothing depends on when the test happens to run.
func putAt(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	put(t, path, body)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, body string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func registryEntry(t *testing.T, dir string, pid int, sessionID, cwd, status, name string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"pid": pid, "sessionId": sessionID, "cwd": cwd, "status": status, "name": name,
		"startedAt": 1794400000000 + int64(pid), "procStart": fmt.Sprint(pid),
		"entrypoint": "claude-vscode", "version": "2.1.322",
	})
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "sessions", fmt.Sprintf("%d.json", pid)), string(raw))
}

const textOK = `{"type":"text","text":"ok"}`

func msg(id, model, ts, block string, in, out, write1h, read int64) string {
	if block == "" {
		block = textOK
	}
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"timestamp":%q,"message":{"id":%q,"model":%q,"content":[%s],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":%d}}}}`+"\n",
		ts, id, model, block, in, out, write1h, read, write1h)
}

func toolUse(name string, input map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"type": "tool_use", "id": "t", "name": name, "input": input})
	return string(raw)
}

func userPrompt(ts, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":%q}}`+"\n", ts, text)
}

// agentFiles writes a two-response subagent transcript and its meta file.
func agentFiles(t *testing.T, sub, id, model, firstTS, lastTS, meta string) {
	t.Helper()
	put(t, filepath.Join(sub, "agent-"+id+".jsonl"),
		msg("msg_"+id+"-1", model, firstTS, "", 1, 10, 0, 1000)+msg("msg_"+id+"-2", model, lastTS, "", 1, 10, 0, 1000))
	if meta != "" {
		putAt(t, filepath.Join(sub, "agent-"+id+".meta.json"), meta, fixedNow)
	}
}

type world struct {
	dir     string // the Claude config directory
	cwd     string // working directory of the "app" project
	project string // its directory under projects/
	sub     string // subagents directory of session sid-a
	svc     *Service
}

func (w world) newService(pricingPath string) *Service {
	return New(Options{
		ConfigDir:   w.dir,
		PricingPath: pricingPath,
		Alive:       func(pid int, _ string) bool { return pid != 300 },
		Now:         func() time.Time { return fixedNow },
		Location:    ict,
	})
}

func fixture(t *testing.T) world {
	t.Helper()
	dir := t.TempDir()
	w := world{dir: dir, cwd: filepath.Join(dir, "work", "app")}
	w.project = filepath.Join(dir, "projects", scan.Slug(w.cwd))
	w.sub = filepath.Join(w.project, "sid-a", "subagents")

	registryEntry(t, dir, 100, "sid-a", w.cwd, "busy", "app-1")
	registryEntry(t, dir, 200, "sid-b", w.cwd, "idle", "app-2")
	registryEntry(t, dir, 300, "sid-dead", filepath.Join(dir, "work", "gone"), "busy", "gone-1")
	registryEntry(t, dir, 400, "sid-c", filepath.Join(dir, "work", "other"), "idle", "other-1")

	put(t, filepath.Join(w.project, "sid-a.jsonl"),
		`{"type":"ai-title","aiTitle":"Fix timeout","sessionId":"sid-a"}`+"\n"+
			// Yesterday in UTC+7 (09:00 on the 11th).
			msg("msg_m1", "claude-opus-5-5", "2026-11-11T02:00:00Z", "", 10, 100, 1000, 10000)+
			// Today (09:59:30 on the 12th).
			msg("msg_m2", "claude-fable-5-2", "2026-11-12T02:59:30Z",
				toolUse("Read", map[string]any{"file_path": filepath.Join(w.cwd, "internal", "x.go")}), 2, 50, 0, 20000)+
			`{"type":"last-prompt","lastPrompt":"sửa lỗi timeout","sessionId":"sid-a"}`+"\n"+
			`{"type":"cost-state","sessionId":"sid-a","totalCostUSD":0.02}`+"\n")
	put(t, filepath.Join(w.project, "sid-b.jsonl"),
		msg("msg_b1", "claude-sonnet-5-5", "2026-11-12T02:30:00Z", "", 100, 200, 0, 0))

	agentFiles(t, w.sub, "done", "claude-opus-5-5", "2026-11-12T01:00:00Z", "2026-11-12T01:05:00Z",
		`{"agentType":"code-reviewer","description":"Code review","requestShape":"background","finished":true}`)
	agentFiles(t, w.sub, "run", "claude-opus-5-5", "2026-11-12T02:50:00Z", "2026-11-12T02:59:30Z",
		`{"agentType":"go-reviewer","description":"Go review","requestShape":"background"}`)
	agentFiles(t, w.sub, "quiet", "claude-opus-5-5", "2026-11-12T02:40:00Z", "2026-11-12T02:55:00Z",
		`{"agentType":"security-audit","description":"Security audit"}`)
	agentFiles(t, w.sub, "stop", "claude-opus-5-5", "2026-11-12T01:30:00Z", "2026-11-12T02:00:00Z", "")
	agentFiles(t, filepath.Join(w.sub, "workflows", "wf_1"), "odd", "claude-next-9", "2026-11-11T05:00:00Z", "2026-11-11T05:01:00Z",
		`{"agentType":"workflow-subagent","description":"read:store","workflowPhase":"Understand","finished":true}`)

	w.svc = w.newService(filepath.Join(dir, "no-override.json"))
	return w
}

func usd(t *testing.T, what string, got float64, wantNano int64) {
	t.Helper()
	if want := float64(wantNano) / 1e9; math.Abs(got-want) > 1e-12 {
		t.Errorf("%s = $%.9f, want $%.9f", what, got, want)
	}
}

func findAgent(t *testing.T, s Session, id string) Agent {
	t.Helper()
	for _, a := range s.Agents {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("agent %q not found", id)
	return Agent{}
}

// sessionA is the fixture's busy session with the subagents.
func sessionA(t *testing.T, snap Snapshot) Session {
	t.Helper()
	for _, p := range snap.Projects {
		for _, s := range p.Sessions {
			if s.ID == "sid-a" && !s.Duplicate {
				return s
			}
		}
	}
	t.Fatal("session sid-a not in the snapshot")
	return Session{}
}

func TestSnapshot(t *testing.T) {
	w := fixture(t)
	snap := w.svc.Snapshot()

	if snap.Today != "2026-11-12" {
		t.Fatalf("today = %q", snap.Today)
	}
	want := Counts{Projects: 2, Sessions: 3, BusySessions: 1, Agents: 5, RunningAgents: 1, QuietAgents: 1}
	if snap.Counts != want {
		t.Fatalf("counts = %+v, want %+v", snap.Counts, want)
	}

	// Projects are ordered by path; the two sessions in the same directory
	// share one project.
	app, other := snap.Projects[0], snap.Projects[1]
	if app.Name != "app" || len(app.Sessions) != 2 || other.Name != "other" || len(other.Sessions) != 1 {
		t.Fatalf("projects = %s(%d), %s(%d)", app.Name, len(app.Sessions), other.Name, len(other.Sessions))
	}
	if other.Sessions[0].HasTranscript || other.Sessions[0].All.TotalTokens != 0 {
		t.Fatalf("a session with no transcript must be listed empty: %+v", other.Sessions[0])
	}

	a := app.Sessions[0]
	if a.Title != "Fix timeout" || a.Status != "busy" || a.LastPrompt != "sửa lỗi timeout" || a.Duplicate {
		t.Errorf("session a = title %q status %q prompt %q duplicate %v", a.Title, a.Status, a.LastPrompt, a.Duplicate)
	}
	if a.Model != "claude-fable-5-2" || a.ModelLabel != "Fable 5.2" || a.ContextTokens != 20052 {
		t.Errorf("session a model=%q label=%q context=%d", a.Model, a.ModelLabel, a.ContextTokens)
	}
	if a.Activity.Kind != "tool" || a.Activity.Tool != "Read" || a.Activity.Detail != "internal/x.go" {
		t.Errorf("session a activity = %+v, want the path relative to the project", a.Activity)
	}
	if a.RecordedCostUSD != 0.02 || a.ActiveAgents != 2 {
		t.Errorf("recorded cost = %v, active agents = %d", a.RecordedCostUSD, a.ActiveAgents)
	}

	// Main thread: one Opus response yesterday, one Fable response today.
	if a.Own.TotalTokens != 31162 {
		t.Errorf("own tokens = %d, want 31162", a.Own.TotalTokens)
	}
	usd(t, "session a own cost", a.Own.CostUSD, 18_056_000)
	// Plus four priced agents (2 responses each) and one agent on an unknown model.
	if a.All.TotalTokens != 41272 || a.All.UnpricedTokens != 2022 {
		t.Errorf("all tokens = %d unpriced = %d, want 41272 and 2022", a.All.TotalTokens, a.All.UnpricedTokens)
	}
	usd(t, "session a total cost", a.All.CostUSD, 21_288_000)
	// Today excludes yesterday's Opus response and yesterday's agent.
	if a.Today.TotalTokens != 20052+4*2022 {
		t.Errorf("today tokens = %d", a.Today.TotalTokens)
	}
	usd(t, "session a today cost", a.Today.CostUSD, 9_248_000)

	usd(t, "project cost", app.All.CostUSD, 23_488_000)
	usd(t, "grand total cost", snap.All.CostUSD, 23_488_000)
	usd(t, "grand total today", snap.TodaySpend.CostUSD, 11_448_000)
	if snap.All.TotalTokens != 41572 || snap.All.Tokens.Requests != 13 {
		t.Errorf("grand total = %d tokens over %d responses", snap.All.TotalTokens, snap.All.Tokens.Requests)
	}
	// The parts of the cost add up to the cost.
	parts := snap.All.CostParts
	usd(t, "sum of cost parts", parts.Input+parts.Output+parts.CacheRead+parts.CacheWrite+parts.WebSearch, 23_488_000)
	if parts.CacheWrite == 0 || parts.CacheRead == 0 || parts.Output == 0 {
		t.Errorf("cost parts = %+v", parts)
	}

	// Agents still working come first, then stopped, then finished.
	var order []string
	for _, ag := range a.Agents {
		order = append(order, ag.ID+":"+ag.Status)
	}
	if got := strings.Join(order, " "); got != "run:running quiet:quiet stop:stopped done:done odd:done" {
		t.Errorf("agent order = %s", got)
	}

	run := findAgent(t, a, "run")
	if run.Type != "go-reviewer" || run.Description != "Go review" || !run.Background || run.ModelLabel != "Opus 5.5" {
		t.Errorf("running agent = %+v", run)
	}
	if run.DurationMs != (10 * time.Minute).Milliseconds() {
		t.Errorf("a running agent's duration counts up to now: got %d ms", run.DurationMs)
	}
	// Its last line was written at 02:59:30, one minute-slot before "now".
	if run.SilentMs != 30_000 || run.ContextTokens != 1011 || run.Pulse[PulseMinutes-2] != 1 {
		t.Errorf("running agent silent=%d context=%d pulse=%v", run.SilentMs, run.ContextTokens, run.Pulse)
	}
	usd(t, "agent cost", run.All.CostUSD, 808_000)

	if d := findAgent(t, a, "done"); d.DurationMs != (5 * time.Minute).Milliseconds() {
		t.Errorf("a finished agent's duration is first to last line: got %d ms", d.DurationMs)
	}
	stop := findAgent(t, a, "stop")
	if stop.DurationMs != (30*time.Minute).Milliseconds() || stop.Type != "" {
		t.Errorf("stopped agent duration=%d type=%q", stop.DurationMs, stop.Type)
	}
	odd := findAgent(t, a, "odd")
	if odd.WorkflowID != "wf_1" || odd.Phase != "Understand" || odd.All.UnpricedTokens != 2022 || odd.All.CostUSD != 0 || odd.Today.TotalTokens != 0 {
		t.Errorf("workflow agent on unknown model = %+v", odd)
	}

	if len(snap.Models) != 4 || snap.Models[0].Label != "Fable 5.2" {
		t.Errorf("models = %+v", snap.Models)
	}
	var unpricedRow bool
	for _, m := range snap.Models {
		if m.Model == "claude-next-9" {
			unpricedRow = !m.Priced && m.All.UnpricedTokens == 2022
		}
	}
	if !unpricedRow {
		t.Error("the unknown model must appear as an unpriced row")
	}
	if len(snap.Warnings) != 1 || !strings.Contains(snap.Warnings[0], "Chưa có giá") {
		t.Errorf("warnings = %q", snap.Warnings)
	}
	if got := app.EditorURI(); !strings.HasPrefix(got, "vscode://file/") || !strings.HasSuffix(got, "/work/app") {
		t.Errorf("editor URI = %q", got)
	}
}

// A forked agent's transcript starts with a copy of the parent response that
// spawned it. That response must be billed once, to the parent.
func TestForkedAgentDoesNotRecountTheParentsResponse(t *testing.T) {
	w := fixture(t)
	before := w.svc.Snapshot()

	put(t, filepath.Join(w.sub, "agent-fork.jsonl"),
		// The copy: same id and usage as the main transcript's msg_m2.
		msg("msg_m2", "claude-fable-5-2", "2026-11-12T02:59:30Z", "", 2, 50, 0, 20000)+
			msg("msg_fork-own", "claude-fable-5-2", "2026-11-12T02:59:40Z", "", 1, 5, 0, 300))
	put(t, filepath.Join(w.sub, "agent-fork.meta.json"), `{"agentType":"fork","description":"Forked work"}`)

	after := w.svc.Snapshot()
	if got := after.All.TotalTokens - before.All.TotalTokens; got != 306 {
		t.Fatalf("total grew by %d tokens, want only the fork's own 306", got)
	}
	fork := findAgent(t, sessionA(t, after), "fork")
	if fork.All.TotalTokens != 306 || fork.All.Tokens.Requests != 1 {
		t.Fatalf("fork's own spend = %+v", fork.All)
	}
	if got := after.All.Tokens.Requests - before.All.Tokens.Requests; got != 1 {
		t.Fatalf("responses grew by %d, want 1", got)
	}
}

// The same session open in two processes is listed twice but counted once.
func TestSessionOpenInTwoProcessesIsCountedOnce(t *testing.T) {
	w := fixture(t)
	before := w.svc.Snapshot()

	registryEntry(t, w.dir, 500, "sid-a", w.cwd, "idle", "app-1-again")
	after := w.svc.Snapshot()

	if after.Counts.Sessions != before.Counts.Sessions+1 {
		t.Fatalf("sessions listed = %d, want one more than %d", after.Counts.Sessions, before.Counts.Sessions)
	}
	if after.All.CostUSD != before.All.CostUSD || after.All.TotalTokens != before.All.TotalTokens ||
		after.Counts.Agents != before.Counts.Agents || after.Counts.RunningAgents != before.Counts.RunningAgents {
		t.Fatalf("totals changed: %v/%d agents %d, were %v/%d agents %d",
			after.All.CostUSD, after.All.TotalTokens, after.Counts.Agents,
			before.All.CostUSD, before.All.TotalTokens, before.Counts.Agents)
	}
	var duplicates int
	for _, s := range after.Projects[0].Sessions {
		if s.ID == "sid-a" && s.Duplicate {
			duplicates++
			if s.All.TotalTokens == 0 {
				t.Error("the second listing should still show the session's own figures")
			}
		}
	}
	if duplicates != 1 {
		t.Fatalf("%d listings marked duplicate, want 1", duplicates)
	}
	if len(after.Models) != len(before.Models) || after.Models[0].All != before.Models[0].All {
		t.Fatal("per-model totals counted the session twice")
	}
}

// An agent written by a Claude Code version that does not record the finished
// flag is still recognised as done once it has handed its report back.
func TestAgentThatHandedBackIsDoneWithoutFinishedFlag(t *testing.T) {
	w := fixture(t)
	put(t, filepath.Join(w.sub, "agent-old.jsonl"),
		msg("msg_old-1", "claude-opus-5-5", "2026-11-12T02:58:00Z", "", 1, 10, 0, 1000)+
			msg("msg_old-2", "claude-opus-5-5", "2026-11-12T02:59:00Z",
				toolUse("SubagentHandback", map[string]any{"message": "All clear."}), 1, 10, 0, 1000))
	put(t, filepath.Join(w.sub, "agent-old.meta.json"), `{"agentType":"code-reviewer","description":"Old-style agent"}`)

	a := findAgent(t, sessionA(t, w.svc.Snapshot()), "old")
	if a.Status != StatusDone || a.Activity.Kind != "result" || a.Activity.Detail != "All clear." {
		t.Fatalf("status = %q, activity = %+v", a.Status, a.Activity)
	}
	if a.DurationMs != time.Minute.Milliseconds() {
		t.Fatalf("duration = %d ms, want first to last line", a.DurationMs)
	}
}

// A finished agent that is asked to continue is working again, whatever its
// finished flag still says.
func TestFinishedAgentAskedToContinueIsWorkingAgain(t *testing.T) {
	w := fixture(t)
	if got := findAgent(t, sessionA(t, w.svc.Snapshot()), "done").Status; got != StatusDone {
		t.Fatalf("before: status = %q", got)
	}

	// The flag was written when the agent first finished, an hour ago.
	meta := filepath.Join(w.sub, "agent-done.meta.json")
	if err := os.Chtimes(meta, fixedNow.Add(-time.Hour), fixedNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	appendTo(t, filepath.Join(w.sub, "agent-done.jsonl"),
		userPrompt("2026-11-12T02:59:40Z", "Check the edge case too")+
			msg("msg_done-3", "claude-opus-5-5", "2026-11-12T02:59:50Z",
				toolUse("Bash", map[string]any{"command": "go test ./..."}), 1, 10, 0, 1000))

	a := findAgent(t, sessionA(t, w.svc.Snapshot()), "done")
	if a.Status != StatusRunning || a.Activity.Tool != "Bash" {
		t.Fatalf("after continuing: status = %q, activity = %+v", a.Status, a.Activity)
	}
}

// An agent waiting on a long tool call is not written off after 15 minutes.
func TestAgentWaitingOnAToolStaysQuiet(t *testing.T) {
	w := fixture(t)
	write := func(id, lastTS string) {
		put(t, filepath.Join(w.sub, "agent-"+id+".jsonl"),
			msg("msg_"+id, "claude-opus-5-5", lastTS, toolUse("Bash", map[string]any{"command": "make test"}), 1, 10, 0, 1000))
	}
	write("build", "2026-11-12T02:20:00Z")  // 40 minutes without a result
	write("lost", "2026-11-11T22:00:00Z")   // 5 hours: no build takes that long
	write("silent", "2026-11-12T02:20:00Z") // same silence, but its tool had returned
	appendTo(t, filepath.Join(w.sub, "agent-silent.jsonl"),
		`{"type":"user","timestamp":"2026-11-12T02:20:00Z","message":{"role":"user","content":[{"type":"tool_result"}]}}`+"\n")

	s := sessionA(t, w.svc.Snapshot())
	if a := findAgent(t, s, "build"); a.Status != StatusQuiet || !a.AwaitingTool {
		t.Errorf("waiting 40 minutes on a tool: status = %q, awaiting = %v", a.Status, a.AwaitingTool)
	}
	if a := findAgent(t, s, "lost"); a.Status != StatusStopped || a.AwaitingTool {
		t.Errorf("waiting 5 hours: status = %q, awaiting = %v", a.Status, a.AwaitingTool)
	}
	if a := findAgent(t, s, "silent"); a.Status != StatusStopped || a.AwaitingTool {
		t.Errorf("silent 40 minutes with nothing pending: status = %q, awaiting = %v", a.Status, a.AwaitingTool)
	}
}

// With no timestamps in the transcript, the file's own time decides whether
// the agent is still alive, so an agent that died at once does not show as
// running forever.
func TestAgentWithoutTimestampsUsesTheFilesTime(t *testing.T) {
	w := fixture(t)
	putAt(t, filepath.Join(w.sub, "agent-fresh.jsonl"), "", fixedNow.Add(-20*time.Second))
	putAt(t, filepath.Join(w.sub, "agent-dead.jsonl"), "", fixedNow.Add(-2*time.Hour))

	s := sessionA(t, w.svc.Snapshot())
	if a := findAgent(t, s, "fresh"); a.Status != StatusRunning {
		t.Errorf("just launched: status = %q", a.Status)
	}
	if a := findAgent(t, s, "dead"); a.Status != StatusStopped {
		t.Errorf("empty for two hours: status = %q", a.Status)
	}
}

func TestSnapshotIsIncremental(t *testing.T) {
	w := fixture(t)
	first := w.svc.Snapshot()
	if len(w.svc.files) != 7 { // two main transcripts and five agents
		t.Fatalf("tracking %d files, want 7", len(w.svc.files))
	}

	// The busy session produces one more response.
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"),
		msg("msg_m3", "claude-fable-5-2", "2026-11-12T02:59:50Z", "", 1, 1, 0, 30000))

	second := w.svc.Snapshot()
	if got := second.All.TotalTokens - first.All.TotalTokens; got != 30002 {
		t.Fatalf("total grew by %d tokens, want 30002", got)
	}
	if got := sessionA(t, second).ContextTokens; got != 30002 {
		t.Fatalf("context = %d, want the new response's 30002", got)
	}
}

// A registry entry that is unreadable for a moment (Claude Code is rewriting
// it) must not make the session vanish or its transcripts be read again.
func TestRegistryBlinkKeepsTheSession(t *testing.T) {
	w := fixture(t)
	first := w.svc.Snapshot()
	main := w.svc.files[filepath.Join(w.project, "sid-a.jsonl")].file

	put(t, filepath.Join(w.dir, "sessions", "100.json"), "")
	second := w.svc.Snapshot()
	if second.Counts != first.Counts || second.All.CostUSD != first.All.CostUSD {
		t.Fatalf("during the blink: counts %+v cost %v, were %+v and %v",
			second.Counts, second.All.CostUSD, first.Counts, first.All.CostUSD)
	}
	if w.svc.files[filepath.Join(w.project, "sid-a.jsonl")].file != main {
		t.Fatal("the transcript's parser state was discarded and rebuilt")
	}
}

func TestEndedSessionIsForgottenAfterAGracePeriod(t *testing.T) {
	w := fixture(t)
	w.svc.Snapshot()

	if err := os.Remove(filepath.Join(w.dir, "sessions", "100.json")); err != nil {
		t.Fatal(err)
	}
	snap := w.svc.Snapshot()
	if snap.Counts.Sessions != 2 || snap.Counts.Agents != 0 {
		t.Fatalf("counts right after the session ended = %+v", snap.Counts)
	}
	// Its parser state is kept for a while in case the session comes back...
	if len(w.svc.files) != 7 {
		t.Fatalf("tracking %d files right after, want all 7 kept", len(w.svc.files))
	}
	// ...and released once it clearly has not.
	for range forgetAfter + 1 {
		w.svc.Snapshot()
	}
	if len(w.svc.files) != 1 || len(w.svc.metas) != 0 {
		t.Fatalf("still tracking %d files and %d metas for an ended session", len(w.svc.files), len(w.svc.metas))
	}
}

func TestSnapshotOnMachineWithoutClaudeCode(t *testing.T) {
	svc := New(Options{
		ConfigDir:   filepath.Join(t.TempDir(), "absent"),
		PricingPath: filepath.Join(t.TempDir(), "none.json"),
		Alive:       func(int, string) bool { return true },
		Now:         func() time.Time { return fixedNow },
		Location:    ict,
	})
	snap := svc.Snapshot()
	if snap.Counts != (Counts{}) || len(snap.Projects) != 0 || len(snap.Warnings) != 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Projects == nil || snap.Models == nil || snap.Warnings == nil {
		t.Fatal("lists must be empty, not null, for the UI")
	}
}

func TestBrokenPricingOverrideIsReported(t *testing.T) {
	w := fixture(t)
	override := filepath.Join(w.dir, "pricing.json")
	put(t, override, `{"models":`)
	snap := w.newService(override).Snapshot()
	var reported bool
	for _, warning := range snap.Warnings {
		reported = reported || strings.Contains(warning, "Bảng giá tuỳ chỉnh bị lỗi")
	}
	if !reported || snap.PricingSource != "built-in" || snap.All.CostUSD == 0 {
		t.Fatalf("warnings=%q source=%q cost=%v", snap.Warnings, snap.PricingSource, snap.All.CostUSD)
	}
}

// Problems in the data that affect the totals are said out loud.
func TestUnreadableDataIsReported(t *testing.T) {
	w := fixture(t)
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"),
		"{not json\n"+
			`{"type":"assistant","timestamp":"2026-11-12T02:59:55Z","message":{"id":"msg_odd","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":"many"}}}`+"\n")
	snap := w.svc.Snapshot()
	var badUsage, badLines bool
	for _, warning := range snap.Warnings {
		badUsage = badUsage || strings.Contains(warning, "1 lượt gọi API")
		badLines = badLines || strings.Contains(warning, "Bỏ qua 1 dòng")
	}
	if !badUsage || !badLines {
		t.Fatalf("warnings = %q", snap.Warnings)
	}
}

func TestModelLabel(t *testing.T) {
	cases := map[string]string{
		"claude-fable-5-2":          "Fable 5.2",
		"claude-opus-5-5[1m]":       "Opus 5.5",
		"claude-haiku-4-5-20251001": "Haiku 4.5",
		"claude-sonnet-5":           "Sonnet 5",
		"claude-opus-5-5-fast":      "claude-opus-5-5-fast",
		"gpt-x":                     "Gpt X",
		"":                          "",
	}
	for in, want := range cases {
		if got := ModelLabel(in); got != want {
			t.Errorf("ModelLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRelativeTo(t *testing.T) {
	cases := []struct{ cwd, path, want string }{
		{`d:\work\app`, `d:\work\app\internal\x.go`, "internal/x.go"},
		{`d:\work\app`, `D:\Work\App\main.go`, "main.go"},
		{`d:\work\app\`, `d:\work\app\main.go`, "main.go"},
		{`d:\work\app`, `d:\work\application\main.go`, `d:\work\application\main.go`},
		{`d:\work\app`, `c:\elsewhere\y.go`, `c:\elsewhere\y.go`},
		{"/home/dev/app", "/home/dev/app/a/b.go", "a/b.go"},
		{"", "relative.go", "relative.go"},
		{`d:\work\app`, `d:\work\app`, `d:\work\app`},
	}
	for _, c := range cases {
		if got := relativeTo(c.cwd, c.path); got != c.want {
			t.Errorf("relativeTo(%q, %q) = %q, want %q", c.cwd, c.path, got, c.want)
		}
	}
}

func TestEditorURI(t *testing.T) {
	cases := map[string]string{
		"/home/dev/my app":     "vscode://file/home/dev/my%20app",
		"/srv/a&b/c#1":         "vscode://file/srv/a&b/c%231",
		"/srv/d" + "ự" + " án": "vscode://file/srv/d%E1%BB%B1%20%C3%A1n",
	}
	for dir, want := range cases {
		if got := (Project{dir: dir}).EditorURI(); got != want {
			t.Errorf("EditorURI(%q) = %q, want %q", dir, got, want)
		}
	}
}
