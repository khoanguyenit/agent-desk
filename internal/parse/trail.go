package parse

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"monitor-agent/internal/clean"
	"monitor-agent/internal/fsx"
)

// Kinds of step in a trail.
const (
	StepPrompt = "prompt" // something said to the agent: its task, or a follow-up
	StepText   = "text"   // something the agent wrote
	StepTool   = "tool"   // a tool call, with its result once that has arrived
	StepReport = "report" // a subagent's final report
)

const (
	// trailProseRunes bounds what is kept of one piece of prose in a trail:
	// enough to follow the agent's reasoning, not the whole message.
	trailProseRunes = 700
	// trailResultRunes bounds what is kept of one tool result.
	trailResultRunes = 400
	// trailWiderBytes is how far back Trail looks when the usual window held
	// no complete line, which happens when the last line is a very large one.
	trailWiderBytes = 8 << 20
)

// Step is one thing that happened in a transcript, prepared for display: prose
// is cut to a few lines and credential-like values are masked, as everywhere
// else on the board.
type Step struct {
	Kind   string
	Tool   string
	Detail string
	// IsPath marks a Detail that is a file path, which the caller may show
	// relative to the project.
	IsPath bool
	// Result is the beginning of a tool's result; Done is false while the
	// tool has not returned, and Failed is true when it returned an error.
	Result string
	Done   bool
	Failed bool
	At     time.Time

	toolUseID string
}

type trailLine struct {
	Type        string          `json:"type"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

type trailBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Trail reads the end of the transcript at path and returns its latest steps,
// oldest first, at most maxSteps of them. Only the last maxBytes of the file
// are read, so the cost does not grow with the length of the session.
// acceptSidechain has the meaning it has for a File: true for a subagent's
// own transcript. earlier reports whether the transcript holds history from
// before the first step returned.
func Trail(path string, acceptSidechain bool, maxBytes int64, maxSteps int) (steps []Step, earlier bool, err error) {
	data, cut, err := readTail(path, maxBytes)
	if err != nil {
		return nil, false, err
	}
	if len(data) == 0 && cut && maxBytes < trailWiderBytes {
		// The window fell inside one very long line. Look once further back.
		if data, cut, err = readTail(path, trailWiderBytes); err != nil {
			return nil, false, err
		}
	}

	byTool := map[string]int{}
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || skipped(line) {
			continue
		}
		var l trailLine
		if !decode(line, &l) || len(l.Message) == 0 {
			continue
		}
		if !acceptSidechain && l.IsSidechain {
			continue
		}
		var m rawMessage
		if !decode(l.Message, &m) {
			continue
		}
		at := parseTime(l.Timestamp)
		switch l.Type {
		case "assistant":
			for _, b := range trailBlocks(m.Content) {
				switch b.Type {
				case "text":
					if t := prose(b.Text); t != "" {
						steps = append(steps, Step{Kind: StepText, Detail: t, At: at})
					}
				case "tool_use":
					if terminalTools[b.Name] {
						steps = append(steps, Step{Kind: StepReport, Detail: prose(report(b.Input)), Done: true, At: at})
						continue
					}
					if _, dup := byTool[b.ID]; dup && b.ID != "" {
						continue
					}
					detail, isPath := toolDetail(b.Input)
					steps = append(steps, Step{
						Kind: StepTool, Tool: clean.Text(b.Name, labelRunes),
						Detail: detail, IsPath: isPath, At: at, toolUseID: b.ID,
					})
					if b.ID != "" {
						byTool[b.ID] = len(steps) - 1
					}
				}
			}
		case "user":
			if l.IsMeta {
				continue
			}
			content := bytes.TrimSpace(m.Content)
			if len(content) > 0 && content[0] == '"' {
				var s string
				if decode(content, &s) {
					if t := prose(s); t != "" {
						steps = append(steps, Step{Kind: StepPrompt, Detail: t, At: at})
					}
				}
				continue
			}
			for _, b := range trailBlocks(content) {
				switch b.Type {
				case "tool_result":
					if i, ok := byTool[b.ToolUseID]; ok {
						steps[i].Result = clean.Block(clean.Redact(resultText(b.Content)), trailResultRunes)
						steps[i].Done, steps[i].Failed = true, b.IsError
					}
				case "text":
					if t := prose(b.Text); t != "" {
						steps = append(steps, Step{Kind: StepPrompt, Detail: t, At: at})
					}
				}
			}
		}
	}

	if maxSteps > 0 && len(steps) > maxSteps {
		steps = steps[len(steps)-maxSteps:]
		cut = true
	}
	return steps, cut, nil
}

// readTail returns the complete lines within the last maxBytes of the file.
// cut reports whether the file starts before what was returned.
func readTail(path string, maxBytes int64) (data []byte, cut bool, err error) {
	fh, st, err := fsx.OpenRegular(path)
	if err != nil {
		return nil, false, err
	}
	defer fh.Close()

	start := max(st.Size()-maxBytes, 0)
	if _, err := fh.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err = io.ReadAll(io.LimitReader(fh, maxBytes))
	if err != nil {
		return nil, false, err
	}
	if start > 0 {
		// The window opens in the middle of a line, unless the byte before it
		// happens to end one.
		if !newlineAt(fh, start-1) {
			i := bytes.IndexByte(data, '\n')
			if i < 0 {
				return nil, true, nil
			}
			data = data[i+1:]
		}
	}
	// A line still being written has no newline yet and is left for next time.
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[:i+1]
	} else {
		data = nil
	}
	return data, start > 0, nil
}

// skipped reports a line of a kind that is large and holds nothing to show.
func skipped(line []byte) bool {
	for _, p := range skipPrefixes {
		if bytes.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func trailBlocks(raw json.RawMessage) []trailBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var blocks []trailBlock
	if !decode(raw, &blocks) {
		return nil
	}
	return blocks
}

// resultText is the text of a tool result, which Claude Code writes either as
// a string or as a list of blocks.
func resultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if decode(raw, &s) {
			return s
		}
		return ""
	}
	var parts []string
	for _, b := range trailBlocks(raw) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// prose prepares a piece of writing for a trail: credential-like values
// masked, line breaks kept, cut to a few lines' worth.
func prose(s string) string {
	return clean.Block(clean.Redact(strings.TrimSpace(s)), trailProseRunes)
}
