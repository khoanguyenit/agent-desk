package parse

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSummarizeAddsUpASession(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "s.jsonl")
	edit := `{"type":"tool_use","id":"t_edit","name":"Edit","input":{"file_path":"d:\\app\\a.go","old_string":"x","new_string":"y"}}`
	git := `{"type":"tool_use","id":"t_git","name":"Bash","input":{"command":"git commit -m \"fix\" && git push","description":"Commit and push"}}`
	write := `{"type":"tool_use","id":"t_new","name":"Write","input":{"file_path":"d:\\app\\b.go","content":"package b\n\nfunc B() {}\n"}}`
	rm := `{"type":"tool_use","id":"t_rm","name":"Bash","input":{"command":"rm -rf build"}}`
	usage := `{"output_tokens":100,"output_tokens_details":{"thinking_tokens":40},"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}`
	resp := func(id, ts, block, extra string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","effort":"xhigh","message":{"id":"` + id + `","model":"claude-fable-5-2","content":[` + block + `],"usage":` + usage + extra + `}}`
	}
	writeFile(t, main, lines(
		`{"type":"user","timestamp":"2026-11-12T03:00:00.000Z","permissionMode":"auto","message":{"role":"user","content":"Sửa lỗi timeout rồi commit"}}`,
		`{"type":"user","timestamp":"2026-11-12T03:00:01.000Z","isMeta":true,"message":{"role":"user","content":"<system-reminder>x</system-reminder>"}}`,
		resp("r1", "2026-11-12T03:00:10.000Z", edit, `,"diagnostics":{"cache_miss_reason":{"type":"previous_message_not_found","cache_missed_input_tokens":5000}}`),
		// The same response, written again for its next content block.
		resp("r1", "2026-11-12T03:00:10.500Z", git, ""),
		`{"type":"user","timestamp":"2026-11-12T03:00:12.000Z","permissionDecision":{"decision":"accept","source":"config","reasonType":"classifier"},"toolUseResult":{"filePath":"d:\\app\\a.go","structuredPatch":[{"lines":[" a","-x","+y","+z"]}]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_edit","content":"ok"}]}}`,
		`{"type":"user","timestamp":"2026-11-12T03:00:20.000Z","permissionDecision":{"decision":"accept","source":"user_temporary","reasonType":"other"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_git","content":"pushed"}]}}`,
		resp("r2", "2026-11-12T03:01:00.000Z", write, ""),
		`{"type":"user","timestamp":"2026-11-12T03:01:01.000Z","permissionDecision":{"decision":"accept","source":"config","reasonType":"mode"},"toolUseResult":{"type":"create","filePath":"d:\\app\\b.go","content":"package b\n\nfunc B() {}\n","structuredPatch":[]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_new","content":"created"}]}}`,
		resp("r3", "2026-11-12T03:02:00.000Z", rm, ""),
		`{"type":"user","timestamp":"2026-11-12T03:02:05.000Z","toolDenialKind":"user-rejected","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_rm","is_error":true,"content":"rejected"}]}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-11-12T03:05:00.000Z","compactMetadata":{"trigger":"auto","preTokens":180000,"postTokens":20000}}`,
		`{"type":"user","timestamp":"2026-11-12T03:05:01.000Z","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued…"}}`,
		`{"type":"user","timestamp":"2026-11-12T03:06:00.000Z","origin":{"kind":"task-notification"},"message":{"role":"user","content":"<task-notification>done</task-notification>"}}`,
		`{"type":"user","timestamp":"2026-11-12T03:07:00.000Z","origin":{"kind":"human"},"message":{"role":"user","content":[{"type":"text","text":"Giờ push lên"}]}}`,
	))
	// A forked agent starts with a copy of its parent's history, then does
	// its own work.
	agent := filepath.Join(dir, "s", "subagents", "agent-fork.jsonl")
	writeFile(t, agent, lines(
		resp("r1", "2026-11-12T03:00:10.000Z", edit, ""),
		`{"type":"user","timestamp":"2026-11-12T03:00:12.000Z","permissionDecision":{"decision":"accept","source":"config","reasonType":"classifier"},"toolUseResult":{"structuredPatch":[{"lines":["-x","+y","+z"]}]},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t_edit","content":"ok"}]}}`,
		resp("r9", "2026-11-12T03:10:00.000Z", `{"type":"tool_use","id":"t_read","name":"Read","input":{"file_path":"d:\\app\\a.go"}}`, ""),
		// Handing the report back is the agent's result, not a tool call.
		resp("r10", "2026-11-12T03:10:30.000Z", `{"type":"tool_use","id":"t_done","name":"StructuredOutput","input":{"ok":true}}`, ""),
	))

	sum, err := Summarize(main, []string{agent, filepath.Join(dir, "missing.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Prompts != 2 {
		t.Errorf("prompts = %d, want the 2 a person wrote", sum.Prompts)
	}
	if sum.ToolCalls != 5 || sum.Tools["Bash"] != 2 || sum.Tools["Edit"] != 1 || sum.Tools["Read"] != 1 {
		t.Errorf("tool calls = %d %v", sum.ToolCalls, sum.Tools)
	}
	if len(sum.Files) != 2 || sum.Files[`d:\app\a.go`] != 1 || sum.Files[`d:\app\b.go`] != 1 {
		t.Errorf("files = %v; a read is not a change", sum.Files)
	}
	if sum.LinesAdded != 2+3 || sum.LinesRemoved != 1 {
		t.Errorf("lines = +%d -%d, want +5 -1", sum.LinesAdded, sum.LinesRemoved)
	}
	if sum.Commits != 1 || sum.Pushes != 1 {
		t.Errorf("commits = %d, pushes = %d", sum.Commits, sum.Pushes)
	}
	if sum.Allowed["classifier"] != 1 || sum.Allowed["you"] != 1 || sum.Allowed["mode"] != 1 || sum.Denied["user-rejected"] != 1 {
		t.Errorf("allowed = %v, denied = %v", sum.Allowed, sum.Denied)
	}
	if sum.ToolTime != 2*time.Second+9500*time.Millisecond+time.Second+5*time.Second {
		t.Errorf("tool time = %v", sum.ToolTime)
	}
	if sum.Responses != 5 || sum.OutputTokens != 500 || sum.ThinkingTokens != 200 || sum.Efforts["xhigh"] != 5 {
		t.Errorf("responses = %d, output = %d, thinking = %d, efforts = %v", sum.Responses, sum.OutputTokens, sum.ThinkingTokens, sum.Efforts)
	}
	if sum.CacheMisses != 1 || sum.CacheMissTokens != 5000 || sum.Compactions != 1 || sum.CompactedFrom != 180000 || sum.CompactedTo != 20000 {
		t.Errorf("cache misses = %d (%d tokens), compactions = %d (%d → %d)", sum.CacheMisses, sum.CacheMissTokens, sum.Compactions, sum.CompactedFrom, sum.CompactedTo)
	}
	if sum.Modes["auto"] != 1 {
		t.Errorf("modes = %v", sum.Modes)
	}
	if !sum.Partial {
		t.Error("a transcript that could not be read is not reported")
	}
	if want := time.Date(2026, 11, 12, 3, 10, 30, 0, time.UTC); !sum.LastAt.Equal(want) {
		t.Errorf("last at = %v", sum.LastAt)
	}
}
