package monitor

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTrailOfMainThreadAndOfAnAgent(t *testing.T) {
	w := fixture(t)
	appendTo(t, filepath.Join(w.project, "sid-a.jsonl"),
		`{"type":"user","timestamp":"2026-11-12T02:59:40Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"package x"}]}}`+"\n"+
			userPrompt("2026-11-12T02:59:50Z", "now run it with API_KEY=sk-live-abcdef123456"))

	main, err := w.svc.Trail("sid-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's two responses (a text, then a Read), and what was added.
	if main.Earlier || main.Hidden || len(main.Steps) != 3 {
		t.Fatalf("main trail: earlier=%v hidden=%v steps=%+v", main.Earlier, main.Hidden, main.Steps)
	}
	read := main.Steps[1]
	if read.Kind != "tool" || read.Tool != "Read" || read.Detail != "internal/x.go" || !read.Done || read.Result != "package x" {
		t.Errorf("read step = %+v; the path should be relative to the project", read)
	}
	if read.AtMs == 0 {
		t.Error("step lost its time")
	}
	last := main.Steps[2]
	if last.Kind != "prompt" || strings.Contains(last.Detail, "sk-live-abcdef123456") || !strings.Contains(last.Detail, "now run it") {
		t.Errorf("prompt step = %+v; the key must be masked and the rest kept", last)
	}

	agent, err := w.svc.Trail("sid-a", "run", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Steps) != 2 || agent.Steps[0].Kind != "text" || agent.Steps[0].Detail != "ok" {
		t.Fatalf("agent trail = %+v", agent.Steps)
	}
	// An agent started by a workflow lives one level deeper and is found too.
	if wf, err := w.svc.Trail("sid-a", "odd", false); err != nil || len(wf.Steps) != 2 {
		t.Fatalf("workflow agent trail: %d steps, err=%v", len(wf.Steps), err)
	}
}

func TestTrailWithContentsHidden(t *testing.T) {
	w := fixture(t)
	tr, err := w.svc.Trail("sid-a", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Hidden || len(tr.Steps) == 0 {
		t.Fatalf("trail = %+v", tr)
	}
	for _, s := range tr.Steps {
		if s.Detail != "" || s.Result != "" {
			t.Errorf("hidden trail shows contents: %+v", s)
		}
	}
	if tr.Steps[1].Tool != "Read" {
		t.Errorf("the tool's name is not a secret and should stay: %+v", tr.Steps[1])
	}
}

func TestTrailOnlyReachesWhatTheBoardShows(t *testing.T) {
	w := fixture(t)
	for name, c := range map[string]struct{ session, agent string }{
		"unknown session":          {"sid-nope", ""},
		"session of a dead pid":    {"sid-dead", ""},
		"unknown agent":            {"sid-a", "nope"},
		"path as a session id":     {`..\..\sid-a`, ""},
		"path as an agent id":      {"sid-a", `..\..\..\sid-b`},
		"session without any file": {"sid-c", ""},
	} {
		tr, err := w.svc.Trail(c.session, c.agent, false)
		if err == nil {
			t.Errorf("%s: no error, %d steps", name, len(tr.Steps))
		}
		if tr.Steps == nil {
			t.Errorf("%s: steps must be an empty list, not null, for the page", name)
		}
	}
}
