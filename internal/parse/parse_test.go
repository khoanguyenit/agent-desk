package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"monitor-agent/internal/usage"
)

// assistant builds one transcript line for an assistant content block.
func assistant(id, model, ts, block, usageJSON string) string {
	return fmt.Sprintf(`{"parentUuid":"p","isSidechain":false,"type":"assistant","uuid":"u-%s","timestamp":%q,"gitBranch":"main","message":{"id":%q,"model":%q,"role":"assistant","stop_reason":"tool_use","content":[%s],"usage":%s}}`,
		id, ts, id, model, block, usageJSON)
}

// toolResult is the user-side line that carries a tool's output back.
func toolResult(ts string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t"}]}}`, ts)
}

// prompt is a user-side line with something typed or sent to the agent.
func prompt(ts, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":%q}}`, ts, text)
}

const (
	thinkingBlock = `{"type":"thinking","thinking":""}`
	textBlock     = `{"type":"text","text":"Đã  xong   phần đầu."}`
	readBlock     = `{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"d:\\proj\\internal\\a.go"}}`
	bashBlock     = `{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./...","description":"Run the tests"}}`
)

func usageJSON(in, out, cacheWrite, cacheRead int64) string {
	return fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":%d},"server_tool_use":{"web_search_requests":0},"speed":"standard"}`,
		in, out, cacheWrite, cacheRead, cacheWrite)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, body string) {
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

func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }

func total(f *File) usage.Tokens {
	var sum usage.Tokens
	for _, tk := range f.Usage {
		sum.Add(tk)
	}
	return sum
}

func mustUpdate(t *testing.T, f *File) {
	t.Helper()
	if err := f.Update(); err != nil {
		t.Fatal(err)
	}
}

func TestResponseRepeatedPerBlockIsCountedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	u1 := usageJSON(2, 500, 1000, 90000)
	u2 := usageJSON(3, 700, 200, 91000)
	writeFile(t, path, lines(
		assistant("msg_1", "claude-opus-5-5", "2026-11-11T08:00:00.000Z", thinkingBlock, u1),
		assistant("msg_1", "claude-opus-5-5", "2026-11-11T08:00:01.000Z", textBlock, u1),
		assistant("msg_1", "claude-opus-5-5", "2026-11-11T08:00:02.000Z", readBlock, u1),
		assistant("msg_2", "claude-opus-5-5", "2026-11-11T08:01:00.000Z", bashBlock, u2),
	))

	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	want := usage.Tokens{Input: 5, Output: 1200, CacheRead: 181000, CacheWrite1h: 1200, Requests: 2}
	if got := total(f); got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	// The context figure is the last response alone: input + cache + output.
	if f.ContextTokens != 3+200+91000+700 {
		t.Fatalf("context = %d", f.ContextTokens)
	}
	if f.ToolUses != 2 {
		t.Fatalf("tool uses = %d, want 2", f.ToolUses)
	}
	if f.Model != "claude-opus-5-5" || f.GitBranch != "main" {
		t.Fatalf("model=%q branch=%q", f.Model, f.GitBranch)
	}
	// The board shows the command that ran, not the agent's caption for it.
	if f.Last.Kind != "tool" || f.Last.Tool != "Bash" || f.Last.Detail != "go test ./..." {
		t.Fatalf("last activity = %+v", f.Last)
	}
	if got := f.LastAt.Sub(f.FirstAt); got != time.Minute {
		t.Fatalf("span = %s", got)
	}
	if !f.HasResponse("msg_1") || !f.HasResponse("msg_2") || f.HasResponse("msg_3") {
		t.Fatal("HasResponse does not reflect the counted responses")
	}
	seen := 0
	f.Responses(func(id string, k Key, tk usage.Tokens) {
		seen++
		if k.Model != "claude-opus-5-5" || tk.Requests != 1 {
			t.Errorf("response %s: key %+v tokens %+v", id, k, tk)
		}
	})
	if seen != 2 {
		t.Fatalf("Responses yielded %d, want 2", seen)
	}
}

func TestUpdateReadsOnlyCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	first := assistant("msg_1", "claude-opus-5-5", "2026-11-11T08:00:00Z", textBlock, usageJSON(1, 10, 0, 100))
	second := assistant("msg_2", "claude-opus-5-5", "2026-11-11T08:00:05Z", textBlock, usageJSON(1, 20, 0, 200))

	// The writer has finished the first line and is halfway through the second.
	writeFile(t, path, first+"\n"+second[:len(second)/2])
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)
	if got := total(f).Requests; got != 1 {
		t.Fatalf("after partial write: %d responses counted, want 1", got)
	}
	if f.BadLines != 0 {
		t.Fatalf("a half-written line was treated as corrupt")
	}
	// Reading again with nothing new must leave the half line alone.
	mustUpdate(t, f)
	if got := total(f).Requests; got != 1 || f.BadLines != 0 {
		t.Fatalf("re-reading the unfinished tail changed the state: %d responses, %d bad lines", got, f.BadLines)
	}

	appendFile(t, path, second[len(second)/2:]+"\n")
	mustUpdate(t, f)
	if got := total(f); got.Requests != 2 || got.Output != 30 {
		t.Fatalf("after completion: %+v", got)
	}

	before := f.Offset()
	mustUpdate(t, f)
	if f.Offset() != before || total(f).Requests != 2 {
		t.Fatal("an update with no new data changed the state")
	}
}

func TestReplacedFileIsReadAgain(t *testing.T) {
	one := func(id string, out int64) string {
		return assistant(id, "claude-opus-5-5", "2026-11-11T08:00:00Z", textBlock, usageJSON(1, out, 0, 100)) + "\n"
	}
	cases := []struct {
		name        string
		before      string
		after       string
		wantOutput  int64
		wantRequest int64
	}{
		{"by a shorter file", one("msg_a", 10) + one("msg_b", 20), one("msg_c", 7), 7, 1},
		// The same length: every digit count is kept, only the content differs.
		{"by a file of the same size", one("msg_a", 11), one("msg_z", 99), 99, 1},
		// Longer, with different content before the old read position.
		{"by a longer file", one("msg_a", 10), one("msg_x", 500) + one("msg_y", 600), 1100, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.jsonl")
			writeFile(t, path, c.before)
			f := NewFile(path, time.UTC, false)
			mustUpdate(t, f)

			// Replace through a new file moved into place, as an atomic
			// writer would, so the path now names a different file.
			tmp := path + ".tmp"
			writeFile(t, tmp, c.after)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
			mustUpdate(t, f)

			got := total(f)
			if got.Output != c.wantOutput || got.Requests != c.wantRequest {
				t.Fatalf("after replacement: %+v, want output %d over %d responses", got, c.wantOutput, c.wantRequest)
			}
			if f.BadLines != 0 {
				t.Fatalf("the reader resumed in the middle of a line: %d bad lines", f.BadLines)
			}
		})
	}
}

// A file rewritten in place (same file, new content) is also detected, by the
// byte before the old read position no longer being a line end.
func TestRewrittenInPlaceIsReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	short := assistant("msg_a", "claude-opus-5-5", "2026-11-11T08:00:00Z", textBlock, usageJSON(1, 10, 0, 100)) + "\n"
	writeFile(t, path, short)
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	longer := assistant("msg_b", "claude-opus-5-5", "2026-11-11T09:00:00Z", readBlock, usageJSON(1, 4000, 0, 100)) + "\n" +
		assistant("msg_c", "claude-opus-5-5", "2026-11-11T09:00:05Z", textBlock, usageJSON(1, 5000, 0, 100)) + "\n"
	writeFile(t, path, longer)
	mustUpdate(t, f)
	if got := total(f); got.Output != 9000 || got.Requests != 2 || f.BadLines != 0 {
		t.Fatalf("after rewrite: %+v, %d bad lines", got, f.BadLines)
	}
}

func TestCacheWriteSplit(t *testing.T) {
	cases := []struct {
		name   string
		usage  string
		w5, w1 int64
	}{
		{"both lifetimes", `{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":300,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":200}}`, 100, 200},
		{"no split recorded falls back to 5m", `{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":300,"cache_read_input_tokens":0}`, 300, 0},
		{"split smaller than the total: remainder is 5m", `{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":300,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":250}}`, 50, 250},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.jsonl")
			writeFile(t, path, assistant("m", "claude-opus-5-5", "2026-11-11T08:00:00Z", textBlock, c.usage)+"\n")
			f := NewFile(path, time.UTC, false)
			mustUpdate(t, f)
			got := total(f)
			if got.CacheWrite5m != c.w5 || got.CacheWrite1h != c.w1 {
				t.Fatalf("5m=%d 1h=%d, want %d and %d", got.CacheWrite5m, got.CacheWrite1h, c.w5, c.w1)
			}
		})
	}
}

func TestUsageIsBucketedByLocalDayModelAndSpeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	fast := `{"input_tokens":1,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"speed":"fast","server_tool_use":{"web_search_requests":2}}`
	writeFile(t, path, lines(
		// 16:59 UTC is 23:59 on the 11th in UTC+7; 17:00 UTC is 00:00 on the 12th.
		assistant("a", "claude-opus-5-5", "2026-11-11T16:59:00Z", textBlock, usageJSON(1, 10, 0, 0)),
		assistant("b", "claude-opus-5-5", "2026-11-11T17:00:00Z", textBlock, usageJSON(1, 20, 0, 0)),
		assistant("c", "claude-fable-5-2", "2026-11-11T17:01:00Z", textBlock, fast),
		// A later block of response "a" arrives after midnight; it stays on the 11th.
		assistant("a", "claude-opus-5-5", "2026-11-11T17:02:00Z", readBlock, usageJSON(1, 10, 0, 0)),
	))
	f := NewFile(path, time.FixedZone("ICT", 7*3600), false)
	mustUpdate(t, f)

	want := map[Key]usage.Tokens{
		{Day: "2026-11-11", Model: "claude-opus-5-5"}:              {Input: 1, Output: 10, Requests: 1},
		{Day: "2026-11-12", Model: "claude-opus-5-5"}:              {Input: 1, Output: 20, Requests: 1},
		{Day: "2026-11-12", Model: "claude-fable-5-2", Fast: true}: {Input: 1, Output: 5, WebSearches: 2, Requests: 1},
	}
	if len(f.Usage) != len(want) {
		t.Fatalf("buckets = %+v", f.Usage)
	}
	for k, w := range want {
		if got := f.Usage[k]; got != w {
			t.Errorf("bucket %+v = %+v, want %+v", k, got, w)
		}
	}
}

func TestCorrectedUsageReplacesEarlierCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, path, lines(
		assistant("m", "claude-opus-5-5", "2026-11-11T08:00:00Z", thinkingBlock, usageJSON(1, 10, 0, 100)),
		assistant("m", "claude-opus-5-5", "2026-11-11T08:00:01Z", textBlock, usageJSON(1, 250, 0, 100)),
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)
	if got := total(f); got.Output != 250 || got.Requests != 1 {
		t.Fatalf("usage = %+v, want the later figure counted once", got)
	}
}

// A usage record in a shape this code does not know must never overwrite a
// count that was read correctly, and must not be counted as zeros.
func TestUnreadableUsageIsLeftOutNotZeroed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	good := usageJSON(1, 250, 800, 100)
	stringTokens := `{"input_tokens":1,"output_tokens":"250","cache_creation_input_tokens":800,"cache_read_input_tokens":100}`
	reshapedCache := `{"input_tokens":1,"output_tokens":250,"cache_creation_input_tokens":800,"cache_read_input_tokens":100,"cache_creation":"1h"}`
	writeFile(t, path, lines(
		assistant("m", "claude-opus-5-5", "2026-11-11T08:00:00Z", thinkingBlock, good),
		assistant("m", "claude-opus-5-5", "2026-11-11T08:00:01Z", textBlock, stringTokens),
		assistant("m", "claude-opus-5-5", "2026-11-11T08:00:02Z", readBlock, reshapedCache),
		assistant("n", "claude-opus-5-5", "2026-11-11T08:00:03Z", textBlock, stringTokens),
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	want := usage.Tokens{Input: 1, Output: 250, CacheRead: 100, CacheWrite1h: 800, Requests: 1}
	if got := total(f); got != want {
		t.Fatalf("usage = %+v, want the one good reading %+v", got, want)
	}
	if f.BadUsage != 3 {
		t.Fatalf("bad usage records = %d, want 3", f.BadUsage)
	}
	// The activity of those lines is still followed.
	if f.Last.Kind != "text" || f.ToolUses != 1 {
		t.Fatalf("last = %+v, tool uses = %d", f.Last, f.ToolUses)
	}
}

func TestUnrelatedAndMalformedLinesAreTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, path, lines(
		`{"type":"api-request-blob","sessionId":"s","hash":"h","message":"not an object"}`,
		`{"type":"file-history-snapshot","messageId":"x","snapshot":{}}`,
		`{"type":"ai-title","aiTitle":"Sửa lỗi timeout cổng thanh toán","sessionId":"s"}`,
		`{"type":"last-prompt","lastPrompt":"sửa   lỗi\n timeout","sessionId":"s"}`,
		`{"type":"cost-state","sessionId":"s","totalCostUSD":2.050788}`,
		`{"type":"user","timestamp":"2026-11-11T08:00:00Z","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","timestamp":12345,"message":"a string where an object was expected"}`,
		`{"type":"assistant","timestamp":"2026-11-11T08:00:01Z","message":{"id":"x","model":"<synthetic>","content":[{"type":"text","text":"API Error"}],"usage":{"input_tokens":0,"output_tokens":0}}}`,
		`{"type":"assistant","timestamp":"2026-11-11T08:00:02Z","message":{"id":"y","model":"claude-opus-5-5","content":[{"type":"text","text":"no usage yet"}],"usage":null}}`,
		`{this is not json`,
		`{"type":"something-new","payload":[1,2,3]}`,
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	if f.Title != "Sửa lỗi timeout cổng thanh toán" {
		t.Errorf("title = %q", f.Title)
	}
	if f.LastPrompt != "sửa lỗi timeout" {
		t.Errorf("last prompt = %q", f.LastPrompt)
	}
	if !f.HasRecordedCost || f.RecordedCostUSD != 2.050788 {
		t.Errorf("recorded cost = %v %v", f.HasRecordedCost, f.RecordedCostUSD)
	}
	if f.BadLines != 1 || f.BadUsage != 0 {
		t.Errorf("bad lines = %d, bad usage = %d; want 1 (the line that is not JSON) and 0", f.BadLines, f.BadUsage)
	}
	if len(f.Usage) != 0 {
		t.Errorf("usage = %+v, want none: placeholder messages carry no real usage", f.Usage)
	}
}

// What reaches the screen is cleaned on the way in: credentials masked,
// control characters dropped, over-long labels cut.
func TestDisplayedTextIsRedactedAndCleaned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	secretCommand := `{"type":"tool_use","id":"t","name":"Bash","input":{"command":"PGPASSWORD=hunter2 psql -h db -c 'select 1'"}}`
	longName := strings.Repeat("N", 500)
	oddTool := fmt.Sprintf(`{"type":"tool_use","id":"t","name":%q,"input":{}}`, longName)
	writeFile(t, path, lines(
		`{"type":"last-prompt","lastPrompt":"dùng token=abcdef123456 để gọi API","sessionId":"s"}`,
		assistant("a", "claude-opus-5-5", "2026-11-11T08:00:00Z", secretCommand, usageJSON(1, 1, 0, 0)),
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	if strings.Contains(f.Last.Detail, "hunter2") || !strings.Contains(f.Last.Detail, "psql -h db") {
		t.Errorf("command shown as %q", f.Last.Detail)
	}
	if strings.Contains(f.LastPrompt, "abcdef123456") {
		t.Errorf("prompt shown as %q", f.LastPrompt)
	}

	appendFile(t, path, assistant("b", "claude-opus-5-5", "2026-11-11T08:00:05Z", oddTool, usageJSON(1, 1, 0, 0))+"\n")
	mustUpdate(t, f)
	if n := len([]rune(f.Last.Tool)); n > labelRunes {
		t.Errorf("tool name kept at %d runes, limit is %d", n, labelRunes)
	}
}

func TestSidechainLinesInMainTranscript(t *testing.T) {
	side := strings.Replace(
		assistant("side", "claude-haiku-5-5", "2026-11-11T08:00:10Z", readBlock, usageJSON(5, 5, 0, 50)),
		`"isSidechain":false`, `"isSidechain":true`, 1)
	body := assistant("main", "claude-opus-5-5", "2026-11-11T08:00:00Z", textBlock, usageJSON(1, 10, 0, 100)) + "\n" + side + "\n"

	t.Run("main transcript counts the usage but keeps the main thread's state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		writeFile(t, path, body)
		f := NewFile(path, time.UTC, false)
		mustUpdate(t, f)
		if got := total(f).Requests; got != 2 {
			t.Fatalf("responses counted = %d, want 2", got)
		}
		if f.Model != "claude-opus-5-5" || f.ContextTokens != 111 || f.ToolUses != 0 || f.Last.Kind != "text" {
			t.Fatalf("main thread state was overwritten: model=%q ctx=%d tools=%d last=%+v", f.Model, f.ContextTokens, f.ToolUses, f.Last)
		}
	})

	t.Run("subagent transcript treats its lines as its own thread", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "agent.jsonl")
		writeFile(t, path, body)
		f := NewFile(path, time.UTC, true)
		mustUpdate(t, f)
		if f.Model != "claude-haiku-5-5" || f.ContextTokens != 60 || f.ToolUses != 1 {
			t.Fatalf("model=%q ctx=%d tools=%d", f.Model, f.ContextTokens, f.ToolUses)
		}
	})
}

func TestToolDetail(t *testing.T) {
	cases := []struct {
		input  string
		want   string
		isPath bool
		note   string
	}{
		{`{"file_path":"d:\\x\\a.go","old_string":"a"}`, `d:\x\a.go`, true, ""},
		// The command is what ran; the agent's caption for it is only a note.
		{`{"command":"rm -rf build && npm test","description":"Run the unit tests"}`, "rm -rf build && npm test", false, "Run the unit tests"},
		{`{"command":"npm test"}`, "npm test", false, ""},
		{`{"command":"","description":"Run the unit tests"}`, "Run the unit tests", false, ""},
		// A subagent's task is told by its description, not its long prompt.
		{`{"description":"Review the diff","prompt":"You are a reviewer…","subagent_type":"code-reviewer"}`, "Review the diff", false, ""},
		{`{"pattern":"TODO","path":"src"}`, "TODO", false, ""},
		{`{"path":"src"}`, "src", true, ""},
		{`{"url":"https://example.com","prompt":"summarise"}`, "https://example.com", false, ""},
		{`{"file_path":123,"command":"ls"}`, "ls", false, ""},
		// What a session that waits for the user asks.
		{`{"questions":[{"question":"Dùng MySQL hay PostgreSQL?","header":"DB","options":[{"label":"MySQL"}]}]}`, "Dùng MySQL hay PostgreSQL?", false, ""},
		{`{"questions":[{"question":"Tên nhánh?"},{"question":"Có push không?"},{"question":"Ai review?"}]}`, "Tên nhánh? (và 2 câu nữa)", false, ""},
		{`{"questions":"not a list"}`, "", false, ""},
		{`{"unknown":"x"}`, "", false, ""},
		{`not json`, "", false, ""},
	}
	for _, c := range cases {
		got, isPath, note := toolDetail([]byte(c.input))
		if got != c.want || isPath != c.isPath || note != c.note {
			t.Errorf("toolDetail(%s) = %q,%v,%q want %q,%v,%q", c.input, got, isPath, note, c.want, c.isPath, c.note)
		}
	}
}

// How an agent's turn ends differs between Claude Code versions. Each form
// must be recognised, and undone if the agent is then asked to continue.
func TestTurnEnd(t *testing.T) {
	handback := `{"type":"tool_use","id":"t9","name":"SubagentHandback","input":{"message":"## Verdict\n\n**Ship it** after fixing ` + "`ctx`" + ` handling.\n\n- one\n- two"}}`
	structured := `{"type":"tool_use","id":"t9","name":"StructuredOutput","input":{"findings":[1,2],"ok":true}}`
	endTurn := strings.Replace(
		assistant("b", "claude-opus-5-5", "2026-11-11T08:01:00Z", `{"type":"text","text":"All clear."}`, usageJSON(1, 1, 0, 0)),
		`"stop_reason":"tool_use"`, `"stop_reason":"end_turn"`, 1)

	cases := []struct {
		name       string
		last       string
		wantKind   string
		wantDetail string
	}{
		{"handback tool", assistant("b", "claude-opus-5-5", "2026-11-11T08:01:00Z", handback, usageJSON(1, 1, 0, 0)),
			"result", "Verdict Ship it after fixing ctx handling. - one - two"},
		{"structured output tool", assistant("b", "claude-opus-5-5", "2026-11-11T08:01:00Z", structured, usageJSON(1, 1, 0, 0)),
			"result", "findings: 1 (và 1 mục nữa) ok: true"},
		{"plain end of turn", endTurn, "text", "All clear."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.jsonl")
			writeFile(t, path, lines(
				prompt("2026-11-11T07:59:00Z", "Review the diff"),
				assistant("a", "claude-opus-5-5", "2026-11-11T08:00:00Z", readBlock, usageJSON(1, 1, 0, 0)),
				toolResult("2026-11-11T08:00:30Z"),
				c.last,
			))
			f := NewFile(path, time.UTC, true)
			mustUpdate(t, f)

			if !f.TurnEnded || f.AwaitingTool {
				t.Fatalf("turn ended = %v, awaiting tool = %v", f.TurnEnded, f.AwaitingTool)
			}
			if f.Last.Kind != c.wantKind || f.Last.Detail != c.wantDetail {
				t.Fatalf("last activity = %+v", f.Last)
			}

			// The result of the terminal tool arriving does not reopen the turn.
			appendFile(t, path, toolResult("2026-11-11T08:01:01Z")+"\n")
			mustUpdate(t, f)
			if !f.TurnEnded {
				t.Fatal("a tool result reopened a finished turn")
			}

			// A new prompt does: the agent has been asked to continue.
			appendFile(t, path, prompt("2026-11-11T08:05:00Z", "One more thing")+"\n")
			mustUpdate(t, f)
			if f.TurnEnded {
				t.Fatal("still marked finished after being asked to continue")
			}
			appendFile(t, path, assistant("c", "claude-opus-5-5", "2026-11-11T08:05:10Z", bashBlock, usageJSON(1, 1, 0, 0))+"\n")
			mustUpdate(t, f)
			if f.TurnEnded || !f.AwaitingTool || f.Last.Kind != "tool" {
				t.Fatalf("after continuing: ended=%v awaiting=%v last=%+v", f.TurnEnded, f.AwaitingTool, f.Last)
			}
		})
	}
}

func TestAwaitingTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.jsonl")
	writeFile(t, path, lines(
		assistant("a", "claude-opus-5-5", "2026-11-11T08:00:00Z", thinkingBlock, usageJSON(1, 1, 0, 0)),
	))
	f := NewFile(path, time.UTC, true)
	mustUpdate(t, f)
	if f.AwaitingTool {
		t.Fatal("thinking is not waiting on a tool")
	}

	appendFile(t, path, assistant("a", "claude-opus-5-5", "2026-11-11T08:00:01Z", bashBlock, usageJSON(1, 1, 0, 0))+"\n")
	mustUpdate(t, f)
	if !f.AwaitingTool || f.TurnEnded {
		t.Fatalf("after a tool call: awaiting=%v ended=%v", f.AwaitingTool, f.TurnEnded)
	}

	appendFile(t, path, toolResult("2026-11-11T08:20:00Z")+"\n")
	mustUpdate(t, f)
	if f.AwaitingTool {
		t.Fatal("still awaiting after the tool's result arrived")
	}
}

func TestOverlongLineIsSkipped(t *testing.T) {
	old := maxLineBytes
	maxLineBytes = 4096
	t.Cleanup(func() { maxLineBytes = old })

	path := filepath.Join(t.TempDir(), "s.jsonl")
	huge := `{"type":"attachment","blob":"` + strings.Repeat("A", 300_000) + `"}`
	writeFile(t, path, lines(
		assistant("a", "claude-opus-5-5", "2026-11-11T08:00:00Z", textBlock, usageJSON(1, 10, 0, 0)),
		huge,
		assistant("b", "claude-opus-5-5", "2026-11-11T08:00:05Z", textBlock, usageJSON(1, 20, 0, 0)),
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)
	if got := total(f); got.Requests != 2 || got.Output != 30 {
		t.Fatalf("usage = %+v, want both responses around the long line", got)
	}
	if f.BadLines != 1 {
		t.Fatalf("bad lines = %d, want the one skipped line", f.BadLines)
	}

	// An unfinished line of any length is waited for, not skipped.
	appendFile(t, path, strings.Repeat("B", 100_000))
	mustUpdate(t, f)
	if f.BadLines != 1 || total(f).Requests != 2 {
		t.Fatalf("an unfinished long tail changed the state: %d bad lines", f.BadLines)
	}
}

func TestPlain(t *testing.T) {
	cases := map[string]string{
		"# Title\ntext":            "Title\ntext",
		"  ### Deep\nmore":         "Deep\nmore",
		"**bold** and `code`":      "bold and code",
		"a #hashtag stays":         "a #hashtag stays",
		"issue #12 and __init__":   "issue #12 and __init__",
		"nothing to change, 100%.": "nothing to change, 100%.",
	}
	for in, want := range cases {
		if got := plain(in); got != want {
			t.Errorf("plain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPulse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, path, lines(
		assistant("a", "claude-opus-5-5", "2026-11-11T08:00:10Z", textBlock, usageJSON(1, 1, 0, 0)),
		toolResult("2026-11-11T08:00:40Z"),
		assistant("b", "claude-opus-5-5", "2026-11-11T08:03:00Z", textBlock, usageJSON(1, 1, 0, 0)),
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	now := time.Date(2026, 11, 11, 8, 4, 30, 0, time.UTC)
	got := f.Pulse(now, 5) // minutes 08:00 .. 08:04
	want := []int{2, 0, 0, 1, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pulse = %v, want %v", got, want)
		}
	}
}

func TestMissingFile(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "absent.jsonl"), time.UTC, false)
	if err := f.Update(); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestDirectoryIsNotATranscript(t *testing.T) {
	f := NewFile(t.TempDir(), time.UTC, false)
	if err := f.Update(); err == nil {
		t.Fatal("expected an error when the path is a directory")
	}
}

// refusal is the line Claude Code writes in the model's place when a usage
// limit is used up, shaped like the ones on disk (2.1.322).
func refusal(id, ts, kind string, resetsAt int64) string {
	info := ""
	if kind != "" {
		info = fmt.Sprintf(`,"apiErrorParams":{"rate_limit_info":{"status":"rejected","rateLimitType":%q,"resetsAt":%d}},"quotaLimits":{"status":"rejected","rateLimitType":%q,"resetsAt":%d}`,
			kind, resetsAt, kind, resetsAt)
	}
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"timestamp":%q,"error":"rate_limit","isApiErrorMessage":true,"apiErrorStatus":429%s,"message":{"id":%q,"model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","content":[{"type":"text","text":"You've hit your weekly limit"}],"usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`,
		ts, info, id)
}

func TestUsageLimitIsKeptUntilTheModelAnswersAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	reset := time.Date(2026, 11, 12, 19, 0, 0, 0, time.UTC) // 02:00 on the 13th in UTC+7
	writeFile(t, path, lines(
		`{"type":"session-owner","sessionId":"s","accountUuid":"0f0e0d0c-1111-2222-3333-444455556666","organizationUuid":"org"}`,
		assistant("msg_1", "claude-fable-5-2", "2026-11-12T16:38:00.000Z", textBlock, usageJSON(1, 1, 0, 0)),
		refusal("msg_2", "2026-11-12T16:39:00.000Z", "seven_day", reset.Unix()),
	))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)

	if f.Limit == nil || f.Limit.Kind != "seven_day" || !f.Limit.ResetsAt.Equal(reset) {
		t.Fatalf("limit = %+v", f.Limit)
	}
	if want := time.Date(2026, 11, 12, 16, 39, 0, 0, time.UTC); !f.Limit.At.Equal(want) {
		t.Errorf("limit hit at %v, want %v", f.Limit.At, want)
	}
	if want := time.Date(2026, 11, 12, 16, 38, 0, 0, time.UTC); !f.RespondedAt.Equal(want) {
		t.Errorf("last real response at %v, want %v", f.RespondedAt, want)
	}
	if f.Account != "0f0e0d0c-1111-2222-3333-444455556666" {
		t.Errorf("account = %q", f.Account)
	}
	// The refusal is not a response: it costs nothing and has no model.
	if got := total(f); got.Requests != 1 || f.Model != "claude-fable-5-2" {
		t.Errorf("usage = %+v, model = %q", got, f.Model)
	}

	// The limit is reset, and the model answers again.
	appendFile(t, path, lines(assistant("msg_3", "claude-fable-5-2", "2026-11-12T17:10:00.000Z", textBlock, usageJSON(1, 1, 0, 0))))
	mustUpdate(t, f)
	if f.Limit != nil {
		t.Fatalf("limit still set after a real response: %+v", f.Limit)
	}
}

// Older versions write the refusal without saying which limit or when it
// lifts. It still counts as one, with nothing made up.
func TestUsageLimitWithoutDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, path, lines(refusal("msg_1", "2026-10-22T00:26:00.000Z", "", 0)))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)
	if f.Limit == nil || f.Limit.Kind != "" || !f.Limit.ResetsAt.IsZero() {
		t.Fatalf("limit = %+v", f.Limit)
	}
}

func TestSessionOwnerIgnoresOddAccountIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeFile(t, path, lines(`{"type":"session-owner","accountUuid":"not an id; rm -rf /"}`))
	f := NewFile(path, time.UTC, false)
	mustUpdate(t, f)
	if f.Account != "" {
		t.Fatalf("account = %q", f.Account)
	}
}


// A workflow's agent returns its result in a shape its workflow chooses. The
// board lays it out field by field instead of showing nothing.
func TestReportOfAStructuredResult(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{`{"summary":"All good."}`, "All good."},
		{`{"file":"map-1.md","written":true,"digest":["Discovery reads three things","Parser acts on five types"],"seeds":[]}`,
			"file: map-1.md\nwritten: true\ndigest: Discovery reads three things (và 1 mục nữa)\nseeds: (trống)"},
		{`{"verdict":{"refuted":false},"claims":[{"id":"B1"},{"id":"B2"}],"count":2}`, "verdict: {…}\nclaims: 2 mục\ncount: 2"},
		{`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9}`, "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\nh: 8\n…"},
		{`["not","an","object"]`, ""},
	}
	for _, c := range cases {
		if got := report([]byte(c.input)); got != c.want {
			t.Errorf("report(%s) =\n%q\nwant\n%q", c.input, got, c.want)
		}
	}
}
