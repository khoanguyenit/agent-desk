package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"monitor-agent/internal/clean"
	"monitor-agent/internal/fsx"
)

// Summary is what a session did, added up over the whole of its transcript
// and its subagents' transcripts: what was asked, what changed, who allowed
// the tool calls, and what made it cost what it did. It holds counts and
// file paths, never what was said.
type Summary struct {
	// Prompts counts what a person wrote to the main thread, leaving out
	// what Claude Code itself put there: notifications from background
	// work, summaries after a compaction, command output.
	Prompts   int
	ToolCalls int
	Tools     map[string]int
	// Files counts the edits made to each file, by its path as written.
	Files        map[string]int
	LinesAdded   int64
	LinesRemoved int64
	// Commits and Pushes count git commit and git push commands that ran
	// without an error.
	Commits int
	Pushes  int
	// ToolTime is the time tool calls took, added up call by call: calls
	// that ran side by side each count in full.
	ToolTime time.Duration
	FirstAt  time.Time
	LastAt   time.Time
	// Allowed counts tool calls by who allowed them: "you", "classifier",
	// "mode", "rule", "hook" or "other". Denied counts the ones refused, by
	// how ("user-rejected", "permission-rule", "automode-blocked"…).
	Allowed map[string]int
	Denied  map[string]int
	// Modes counts the permission modes the user's lines were written in.
	Modes map[string]int
	// Compactions counts the times the context was summarised to make room;
	// CompactedFrom and CompactedTo are the sizes around the last one.
	Compactions   int
	CompactedFrom int64
	CompactedTo   int64
	// CacheMisses counts responses for which the cached context could not be
	// used; CacheMissTokens is what had to be sent again because of it, when
	// Claude Code recorded it.
	CacheMisses     int
	CacheMissTokens int64
	Responses       int
	OutputTokens    int64
	ThinkingTokens  int64
	// Efforts counts responses by the effort level they were asked at.
	Efforts map[string]int
	// Partial is true when a transcript could not be read whole.
	Partial bool
}

type sumLine struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	Timestamp        string `json:"timestamp"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Effort           string `json:"effort"`
	PermissionMode   string `json:"permissionMode"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	CompactMetadata *struct {
		PreTokens  int64 `json:"preTokens"`
		PostTokens int64 `json:"postTokens"`
	} `json:"compactMetadata"`
	PermissionDecision *struct {
		Decision   string `json:"decision"`
		Source     string `json:"source"`
		ReasonType string `json:"reasonType"`
	} `json:"permissionDecision"`
	ToolDenialKind string          `json:"toolDenialKind"`
	ToolUseResult  json.RawMessage `json:"toolUseResult"`
	Message        json.RawMessage `json:"message"`
}

type sumMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Output  int64 `json:"output_tokens"`
		Details *struct {
			Thinking int64 `json:"thinking_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
	Diagnostics *struct {
		CacheMiss *struct {
			Tokens int64 `json:"cache_missed_input_tokens"`
		} `json:"cache_miss_reason"`
	} `json:"diagnostics"`
}

type sumBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// editTools change files, and name the file in their input.
var editTools = map[string]string{
	"Edit":         "file_path",
	"MultiEdit":    "file_path",
	"Write":        "file_path",
	"NotebookEdit": "notebook_path",
}

var (
	gitCommit = regexp.MustCompile(`\bgit(?:\s+-[cC]\s+\S+)*\s+commit\b`)
	gitPush   = regexp.MustCompile(`\bgit(?:\s+-[cC]\s+\S+)*\s+push\b`)
)

// summing is the state of one Summarize call across its files.
type summing struct {
	sum Summary
	// Each response, tool call and tool result is counted once, wherever it
	// appears: a forked agent's transcript starts with a copy of its
	// parent's history.
	responses map[string]bool
	calls     map[string]pendingCall
	results   map[string]bool
}

type pendingCall struct {
	at  time.Time
	git string // "commit", "push", "both" or ""
}

// Summarize adds up a session from its main transcript and its subagents'.
// Every file is read in full, so it is meant to be asked for, not run on
// every refresh. A file that cannot be opened is left out and the summary
// marked partial; an error is returned only when the main one cannot be read.
func Summarize(main string, agents []string) (Summary, error) {
	s := &summing{
		sum: Summary{
			Tools: map[string]int{}, Files: map[string]int{}, Allowed: map[string]int{},
			Denied: map[string]int{}, Modes: map[string]int{}, Efforts: map[string]int{},
		},
		responses: map[string]bool{},
		calls:     map[string]pendingCall{},
		results:   map[string]bool{},
	}
	if err := s.file(main, true); err != nil {
		return s.sum, err
	}
	for _, path := range agents {
		if err := s.file(path, false); err != nil {
			s.sum.Partial = true
		}
	}
	return s.sum, nil
}

func (s *summing) file(path string, main bool) error {
	fh, _, err := fsx.OpenRegular(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	r := bufio.NewReaderSize(fh, readBufferSize)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// A last line without its newline is still being written.
			if errors.Is(err, io.EOF) {
				return nil
			}
			s.sum.Partial = true
			return nil
		}
		if len(line) > maxLineBytes || skipped(line) {
			continue
		}
		s.line(bytes.TrimSpace(line), main)
	}
}

func (s *summing) line(raw []byte, main bool) {
	var l sumLine
	if len(raw) == 0 || !decode(raw, &l) {
		return
	}
	at := parseTime(l.Timestamp)
	if !at.IsZero() {
		if s.sum.FirstAt.IsZero() || at.Before(s.sum.FirstAt) {
			s.sum.FirstAt = at
		}
		if at.After(s.sum.LastAt) {
			s.sum.LastAt = at
		}
	}
	switch l.Type {
	case "assistant":
		s.assistant(l, at)
	case "user":
		s.user(l, at, main)
	case "system":
		if l.Subtype == "compact_boundary" && l.CompactMetadata != nil {
			s.sum.Compactions++
			s.sum.CompactedFrom, s.sum.CompactedTo = l.CompactMetadata.PreTokens, l.CompactMetadata.PostTokens
		}
	}
}

func (s *summing) assistant(l sumLine, at time.Time) {
	var m sumMessage
	if len(l.Message) == 0 || !decode(l.Message, &m) {
		return
	}
	// The same response is written once per content block; its figures are
	// the response's, taken once.
	if m.ID != "" && m.Model != "" && !strings.HasPrefix(m.Model, "<") && !s.responses[m.ID] {
		s.responses[m.ID] = true
		s.sum.Responses++
		if m.Usage != nil {
			s.sum.OutputTokens += nonNegative(m.Usage.Output)
			if m.Usage.Details != nil {
				s.sum.ThinkingTokens += nonNegative(m.Usage.Details.Thinking)
			}
		}
		if m.Diagnostics != nil && m.Diagnostics.CacheMiss != nil {
			s.sum.CacheMisses++
			s.sum.CacheMissTokens += nonNegative(m.Diagnostics.CacheMiss.Tokens)
		}
		if l.Effort != "" {
			s.sum.Efforts[clean.Text(l.Effort, labelRunes)]++
		}
	}
	var blocks []sumBlock
	if !decode(bytes.TrimSpace(m.Content), &blocks) {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_use" || b.ID == "" {
			continue
		}
		if _, seen := s.calls[b.ID]; seen || s.results[b.ID] {
			continue
		}
		name := clean.Text(b.Name, labelRunes)
		s.sum.ToolCalls++
		s.sum.Tools[name]++
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(b.Input, &fields)
		call := pendingCall{at: at}
		if key, ok := editTools[b.Name]; ok {
			var p string
			if json.Unmarshal(fields[key], &p) == nil && p != "" {
				s.sum.Files[clean.Text(p, 300)]++
			}
		}
		if b.Name == "Bash" || b.Name == "PowerShell" {
			var cmd string
			if json.Unmarshal(fields["command"], &cmd) == nil {
				switch c, p := gitCommit.MatchString(cmd), gitPush.MatchString(cmd); {
				case c && p:
					call.git = "both"
				case c:
					call.git = "commit"
				case p:
					call.git = "push"
				}
			}
		}
		s.calls[b.ID] = call
	}
}

func (s *summing) user(l sumLine, at time.Time, main bool) {
	if l.PermissionMode != "" {
		s.sum.Modes[clean.Text(l.PermissionMode, labelRunes)]++
	}
	var m sumMessage
	if len(l.Message) == 0 || !decode(l.Message, &m) {
		return
	}
	content := bytes.TrimSpace(m.Content)
	if len(content) > 0 && content[0] == '"' {
		var text string
		if main && decode(content, &text) && s.typed(l, text) {
			s.sum.Prompts++
		}
		return
	}
	var blocks []sumBlock
	if !decode(content, &blocks) {
		return
	}
	prompt := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			prompt = prompt || s.typed(l, b.Text)
		case "tool_result":
			s.result(l, b, at)
		}
	}
	if main && prompt {
		s.sum.Prompts++
	}
}

// typed reports whether text on a user line was written by a person rather
// than put there by Claude Code.
func (s *summing) typed(l sumLine, text string) bool {
	if l.IsMeta || l.IsCompactSummary || l.IsSidechain {
		return false
	}
	if l.Origin != nil && l.Origin.Kind != "" && l.Origin.Kind != "human" {
		return false
	}
	t := strings.TrimSpace(text)
	return t != "" && !strings.HasPrefix(t, "<") && !strings.HasPrefix(t, "[Request interrupted")
}

func (s *summing) result(l sumLine, b sumBlock, at time.Time) {
	if b.ToolUseID == "" || s.results[b.ToolUseID] {
		return
	}
	s.results[b.ToolUseID] = true
	call, known := s.calls[b.ToolUseID]
	if known && !call.at.IsZero() && at.After(call.at) {
		s.sum.ToolTime += at.Sub(call.at)
	}

	switch d := l.PermissionDecision; {
	case l.ToolDenialKind != "":
		s.sum.Denied[clean.Text(l.ToolDenialKind, labelRunes)]++
	case d != nil && d.Decision == "reject":
		s.sum.Denied["rejected"]++
	case d != nil && d.Decision == "accept":
		s.sum.Allowed[allowedBy(d.Source, d.ReasonType)]++
	}

	if !b.IsError && known {
		switch call.git {
		case "both":
			s.sum.Commits++
			s.sum.Pushes++
		case "commit":
			s.sum.Commits++
		case "push":
			s.sum.Pushes++
		}
	}
	if !b.IsError && len(l.ToolUseResult) > 0 && l.ToolUseResult[0] == '{' {
		added, removed := changedLines(l.ToolUseResult)
		s.sum.LinesAdded += added
		s.sum.LinesRemoved += removed
	}
}

// allowedBy names who let a tool call run.
func allowedBy(source, reason string) string {
	switch {
	case source == "user_temporary":
		return "you"
	case source == "hook" || reason == "hook":
		return "hook"
	case reason == "classifier", reason == "mode", reason == "rule":
		return reason
	default:
		return "other"
	}
}

// changedLines counts the lines an edit put in and took out, from the patch
// Claude Code records with its result. A file written new has no patch: all
// its lines are put in.
func changedLines(raw json.RawMessage) (added, removed int64) {
	var r struct {
		Type  string `json:"type"`
		Patch []struct {
			Lines []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	if !decode(raw, &r) {
		return 0, 0
	}
	for _, hunk := range r.Patch {
		for _, line := range hunk.Lines {
			switch {
			case strings.HasPrefix(line, "+"):
				added++
			case strings.HasPrefix(line, "-"):
				removed++
			}
		}
	}
	if r.Type == "create" && len(r.Patch) == 0 {
		var c struct {
			Content string `json:"content"`
		}
		if decode(raw, &c) && c.Content != "" {
			added += int64(strings.Count(strings.TrimSuffix(c.Content, "\n"), "\n") + 1)
		}
	}
	return added, removed
}
