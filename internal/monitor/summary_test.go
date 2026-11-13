package monitor

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// editUse is an Edit call with an id of its own: the summary counts each call
// once by its id, and the fixture's other calls share the id "t".
func editUse(file string) string {
	raw, _ := json.Marshal(map[string]any{"type": "tool_use", "id": "t_edit", "name": "Edit",
		"input": map[string]any{"file_path": file, "old_string": "a", "new_string": "b"}})
	return string(raw)
}

func TestSummaryOfASessionOnTheBoard(t *testing.T) {
	w := fixture(t)
	file := filepath.Join(w.cwd, "internal", "x.go")
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"),
		userPrompt("2026-11-12T02:59:40Z", "Sửa timeout")+
			msg("msg_m3", "claude-fable-5-2", "2026-11-12T02:59:45Z",
				editUse(file), 1, 1, 0, 0))
	w.svc.Snapshot()

	sum, err := w.svc.Summary("sid-a", false)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Prompts != 1 || sum.Agents != 5 || sum.Workflows != 1 {
		t.Errorf("prompts = %d, agents = %d, workflows = %d", sum.Prompts, sum.Agents, sum.Workflows)
	}
	// The board writes a path inside the project with forward slashes.
	want := "internal/x.go"
	if sum.FilesChanged != 1 || len(sum.Files) != 1 || sum.Files[0] != (Count{Name: want, N: 1}) {
		t.Errorf("files = %d %+v, want %q relative to the project", sum.FilesChanged, sum.Files, want)
	}

	hidden, err := w.svc.Summary("sid-a", true)
	if err != nil || !hidden.Hidden || len(hidden.Files) != 0 || hidden.FilesChanged != 1 {
		t.Errorf("hidden summary = %+v, %v; names go, counts stay", hidden, err)
	}

	if _, err := w.svc.Summary("not-on-the-board", false); err == nil {
		t.Error("a session the board does not show was summarised")
	}
}
