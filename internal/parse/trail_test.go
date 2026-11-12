package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trailKinds(steps []Step) string {
	var out []string
	for _, s := range steps {
		switch s.Kind {
		case StepTool:
			state := "running"
			if s.Done && s.Failed {
				state = "failed"
			} else if s.Done {
				state = "done"
			}
			out = append(out, "tool:"+s.Tool+":"+state)
		default:
			out = append(out, s.Kind)
		}
	}
	return strings.Join(out, " ")
}

func TestTrailFollowsATaskFromPromptToReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	writeFile(t, path, lines(
		`{"type":"user","isSidechain":true,"timestamp":"2026-11-12T03:00:00.000Z","message":{"role":"user","content":"Review the transfer handler for races."}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-12T03:00:02.000Z","message":{"id":"msg_1","content":[{"type":"thinking","thinking":""}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-12T03:00:03.000Z","message":{"id":"msg_1","content":[{"type":"text","text":"I'll start with the handler.\n\nThen the tests."}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-12T03:00:04.000Z","message":{"id":"msg_1","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"D:\\work\\api\\internal\\transfer\\handler.go"}}]}}`,
		`{"type":"user","isSidechain":true,"timestamp":"2026-11-12T03:00:05.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"package transfer\n\nfunc Handle() {}"}]}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-12T03:00:06.000Z","message":{"id":"msg_2","content":[{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"go test ./internal/transfer/","description":"Run the transfer tests"}}]}}`,
		`{"type":"user","isSidechain":true,"timestamp":"2026-11-12T03:00:09.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","is_error":true,"content":"FAIL: TestHandle"}]}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-11-12T03:00:10.000Z","message":{"id":"msg_3","content":[{"type":"tool_use","id":"toolu_3","name":"Grep","input":{"pattern":"sync.Mutex"}}]}}`,
	))

	steps, earlier, err := Trail(path, true, 1<<20, 50)
	if err != nil {
		t.Fatal(err)
	}
	if earlier {
		t.Error("the whole file was read, yet earlier history is reported")
	}
	if got, want := trailKinds(steps), "prompt text tool:Read:done tool:Bash:failed tool:Grep:running"; got != want {
		t.Fatalf("steps = %s\nwant    %s", got, want)
	}
	if steps[0].Detail != "Review the transfer handler for races." {
		t.Errorf("prompt = %q", steps[0].Detail)
	}
	if steps[1].Detail != "I'll start with the handler.\n\nThen the tests." {
		t.Errorf("prose lost its line breaks: %q", steps[1].Detail)
	}
	read := steps[2]
	if !read.IsPath || !strings.HasSuffix(read.Detail, `handler.go`) || !strings.Contains(read.Result, "func Handle()") {
		t.Errorf("read step = %+v", read)
	}
	if steps[3].Detail != "Run the transfer tests" || steps[3].Result != "FAIL: TestHandle" {
		t.Errorf("bash step = %+v", steps[3])
	}
	if steps[1].At.IsZero() || !steps[4].At.After(steps[1].At) {
		t.Errorf("times not kept: %v, %v", steps[1].At, steps[4].At)
	}

	// The agent hands its report back.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, `{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_3","content":"internal/transfer/handler.go:12"}]}}`)
	fmt.Fprintln(f, `{"type":"assistant","isSidechain":true,"message":{"id":"msg_4","content":[{"type":"tool_use","id":"toolu_4","name":"SubagentHandback","input":{"message":"One race: the balance is read outside the lock."}}]}}`)
	f.Close()

	steps, _, err = Trail(path, true, 1<<20, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trailKinds(steps), "prompt text tool:Read:done tool:Bash:failed tool:Grep:done report"; got != want {
		t.Fatalf("steps = %s\nwant    %s", got, want)
	}
	if last := steps[len(steps)-1]; last.Detail != "One race: the balance is read outside the lock." {
		t.Errorf("report = %q", last.Detail)
	}
}

func TestTrailMasksAndBoundsWhatItShows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.jsonl")
	long := strings.Repeat("word ", 400)
	writeFile(t, path, lines(
		`{"type":"user","message":{"role":"user","content":"deploy with DB_PASSWORD=hunter2 please"}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"`+long+`"}]}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"curl -H 'Authorization: Bearer abc.def.ghi' https://example.com"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"token=supersecretvalue123\n`+strings.Repeat("x", 2000)+`"}]}}`,
	))
	steps, _, err := Trail(path, false, 1<<20, 50)
	if err != nil || len(steps) != 3 {
		t.Fatalf("steps = %d, err = %v", len(steps), err)
	}
	for _, s := range steps {
		for _, secret := range []string{"hunter2", "abc.def.ghi", "supersecretvalue123"} {
			if strings.Contains(s.Detail, secret) || strings.Contains(s.Result, secret) {
				t.Errorf("%s step shows %q: detail=%q result=%.60q", s.Kind, secret, s.Detail, s.Result)
			}
		}
	}
	if n := len([]rune(steps[1].Detail)); n > trailProseRunes+2 {
		t.Errorf("prose kept %d runes, limit is %d", n, trailProseRunes)
	}
	if n := len([]rune(steps[2].Result)); n > trailResultRunes+2 {
		t.Errorf("result kept %d runes, limit is %d", n, trailResultRunes)
	}
}

func TestTrailReadsOnlyTheEndOfALongTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.jsonl")
	var b strings.Builder
	for i := range 400 {
		fmt.Fprintf(&b, `{"type":"assistant","message":{"id":"m%d","content":[{"type":"text","text":"step %d %s"}]}}`+"\n", i, i, strings.Repeat("-", 200))
	}
	// A line that is still being written: no newline yet.
	b.WriteString(`{"type":"assistant","message":{"id":"half","content":[{"type":"text","text":"unfinished`)
	writeFile(t, path, b.String())

	steps, earlier, err := Trail(path, false, 16<<10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !earlier {
		t.Error("earlier history exists and was not reported")
	}
	if len(steps) != 20 {
		t.Fatalf("%d steps, want the last 20", len(steps))
	}
	if !strings.HasPrefix(steps[len(steps)-1].Detail, "step 399 ") || !strings.HasPrefix(steps[0].Detail, "step 380 ") {
		t.Errorf("window is steps %q .. %q", steps[0].Detail[:10], steps[len(steps)-1].Detail[:10])
	}
	for _, s := range steps {
		if strings.Contains(s.Detail, "unfinished") {
			t.Error("a half-written line was shown")
		}
	}

	// A window that opens inside a line starts at the next whole one.
	steps, _, err = Trail(path, false, 700, 20)
	if err != nil || len(steps) == 0 || len(steps) > 3 {
		t.Fatalf("small window: %d steps, err=%v", len(steps), err)
	}
	if !strings.HasPrefix(steps[len(steps)-1].Detail, "step 399 ") {
		t.Errorf("small window ends at %q", steps[len(steps)-1].Detail[:10])
	}
}

func TestTrailLooksFurtherBackPastOneHugeLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.jsonl")
	writeFile(t, path, lines(
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"big.bin"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"`+strings.Repeat("z", 200<<10)+`"}]}}`,
	))
	steps, _, err := Trail(path, false, 64<<10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := trailKinds(steps); got != "tool:Read:done" {
		t.Fatalf("steps = %q; the window fell inside the long line and nothing further back was read", got)
	}
}

func TestTrailLeavesOutWhatIsNotTheAgentsOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.jsonl")
	writeFile(t, path, lines(
		`not json at all`,
		`{"type":"file-history-snapshot","messageId":"x","snapshot":{}}`,
		`{"type":"ai-title","aiTitle":"A title"}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>ignore</local-command-caveat>"}}`,
		`{"type":"assistant","isSidechain":true,"message":{"id":"s1","content":[{"type":"text","text":"a subagent speaking in the main file"}]}}`,
		`{"type":"assistant","message":{"id":"m1","content":"not a list"}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"unknown","content":"x"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"and now the tests"}]}}`,
	))
	steps, _, err := Trail(path, false, 1<<20, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trailKinds(steps), "tool:Bash:running prompt"; got != want {
		t.Fatalf("steps = %s\nwant    %s", got, want)
	}

	if _, _, err := Trail(filepath.Join(t.TempDir(), "absent.jsonl"), false, 1<<20, 50); err == nil {
		t.Error("a missing file gave no error")
	}
}
