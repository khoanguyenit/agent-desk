package agent

import (
	"bytes"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"monitor-agent/internal/clean"
)

// ---- what this program writes ------------------------------------------------

func userMessage(text string) any {
	return map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": text},
		"parent_tool_use_id": nil,
	}
}

const denyMessage = "Người dùng từ chối thao tác này."

// permissionResponse answers a can_use_tool request. An allow hands the
// tool's input back exactly as it was received; a deny carries a message the
// agent sees. Without an input to hand back there is nothing to allow, and
// the answer is a deny: an empty input is never made up in its place.
func permissionResponse(requestID string, allow bool, input json.RawMessage) any {
	decision := map[string]any{"behavior": "deny", "message": denyMessage, "interrupt": false}
	if allow && isObject(input) {
		decision = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	return map[string]any{
		"type":     "control_response",
		"response": map[string]any{"subtype": "success", "request_id": requestID, "response": decision},
	}
}

func controlError(requestID, message string) any {
	return map[string]any{
		"type":     "control_response",
		"response": map[string]any{"subtype": "error", "request_id": requestID, "error": message},
	}
}

func interruptRequest(requestID string) any {
	return map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request":    map[string]any{"subtype": "interrupt"},
	}
}

func isObject(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	var fields map[string]json.RawMessage
	return json.Unmarshal(raw, &fields) == nil
}

// ---- what Claude Code writes -------------------------------------------------

type wireLine struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	RequestID      string          `json:"request_id"`
	Request        json.RawMessage `json:"request"`
	SessionID      string          `json:"session_id"`
	Model          string          `json:"model"`
	PermissionMode string          `json:"permissionMode"`
	Event          json.RawMessage `json:"event"`
	Result         string          `json:"result"`
	IsError        bool            `json:"is_error"`
	Cost           float64         `json:"total_cost_usd"`
	NumTurns       int             `json:"num_turns"`
	// Parent is set on messages that belong to a subagent rather than to the
	// conversation the user is having.
	Parent  *string `json:"parent_tool_use_id"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type wireRequest struct {
	Subtype   string          `json:"subtype"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

type wireEvent struct {
	Type         string `json:"type"`
	ContentBlock struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

func blocksOf(raw json.RawMessage) []wireBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		return []wireBlock{{Type: "text", Text: s}}
	}
	var blocks []wireBlock
	_ = json.Unmarshal(raw, &blocks)
	return blocks
}

// Claude Code's own marks for a turn the user stopped.
var interruptedMarks = []string{"[Request interrupted by user]", "[Request interrupted by user for tool use]"}

// consume applies one line of Claude Code's output to the session and reports
// whether anything the page shows changed. The caller holds s.mu. Replies the
// line calls for are left in s.outbox: writing them here, with the lock held,
// could block on a process that has stopped reading.
func (s *session) consume(line []byte, now time.Time) bool {
	if s.over() || s.abort {
		// A line that arrives after the session was declared over changes
		// nothing: an ended session stays ended. The same goes for one that
		// is being stopped because it must not go on.
		return false
	}
	var l wireLine
	if json.Unmarshal(line, &l) != nil && l.Type == "" {
		return false
	}
	switch l.Type {
	case "system":
		if l.Subtype != "init" {
			return false
		}
		if l.Model != "" {
			s.model = clean.Text(l.Model, 80)
		}
		// The session must be in the mode the user chose. Settings of the
		// project or of the machine can ask for another one; if Claude Code
		// reports anything but the mode requested, the session is stopped
		// rather than left to act under rules the user did not pick.
		if want, known := reportedModes[s.mode]; known && l.PermissionMode != "" && !slices.Contains(want, l.PermissionMode) {
			s.problem = "Claude Code khởi động ở chế độ quyền \"" + clean.Text(l.PermissionMode, 40) +
				"\" thay vì chế độ bạn chọn, nhiều khả năng do cấu hình của project hoặc của máy ghi đè. App đã dừng session này."
			s.abort = true
			s.touch()
			return true
		}
		if l.SessionID != "" && !strings.EqualFold(l.SessionID, s.id) {
			s.claudeID = clean.Text(l.SessionID, 128)
		}
		// The model is the user's choice too, but a lesser one than the
		// mode: a session on another model is said to be, not stopped.
		if s.wantModel != "" && s.model != "" && !sameModel(s.wantModel, s.model) {
			s.add(Item{Kind: KindNotice, Text: "Claude Code đang dùng model " + s.model + ", không phải model bạn chọn (" + s.wantModel + ")."}, now)
			s.wantModel = ""
		}
		if s.state == StateStarting {
			s.state = StateWorking
			s.setActivity(ActivityWorking, "")
		}
		s.touch()
		return true

	case "stream_event":
		if l.Parent != nil {
			return false
		}
		return s.onStreamEvent(l.Event, now)

	case "assistant":
		if l.Parent != nil {
			return false
		}
		changed := false
		for _, b := range blocksOf(l.Message.Content) {
			switch b.Type {
			case "text":
				s.finishText(clean.Block(b.Text, maxTextRunes), now)
				changed = true
			case "tool_use":
				s.endLive()
				it := Item{
					Kind:      KindTool,
					Tool:      clean.Text(b.Name, 80),
					Input:     describeInput(b.Name, b.Input),
					toolUseID: b.ID,
				}
				if c, _, ok := changeOf(b.Name, b.Input, false); ok {
					it.Change = &c
				}
				s.add(it, now)
				s.setActivity(ActivityTool, it.Tool)
				changed = true
			}
		}
		if changed && s.state != StateWaiting {
			s.state = StateWorking
		}
		return changed

	case "user":
		if l.Parent != nil {
			return false
		}
		changed := false
		for _, b := range blocksOf(l.Message.Content) {
			switch b.Type {
			case "tool_result":
				if it := s.findTool(b.ToolUseID); it != nil {
					it.Result = clean.Block(resultText(b.Content), maxResultRune)
					it.Done, it.IsError = true, b.IsError
					s.rev++
					it.Rev = s.rev
					// The result goes back to the model, which has the turn again.
					s.setActivity(ActivityWorking, "")
					changed = true
				}
			case "text":
				// Claude Code's own notes, such as the interruption marker.
				t := clean.Block(b.Text, 400)
				if slices.Contains(interruptedMarks, strings.TrimSpace(t)) {
					t = "Bạn đã dừng lượt này."
				}
				if t != "" {
					s.endLive()
					s.add(Item{Kind: KindNotice, Text: t}, now)
					changed = true
				}
			}
		}
		return changed

	case "control_request":
		return s.onControlRequest(l, now)

	case "control_cancel_request":
		// Claude Code no longer waits for an answer to this request.
		if i := s.pendingIndex(l.RequestID); i >= 0 {
			s.pending = slices.Delete(s.pending, i, i+1)
			if len(s.pending) == 0 && s.state == StateWaiting {
				s.state = StateWorking
			}
			s.touch()
			return true
		}
		return false

	case "result":
		s.endLive()
		// The reported cost is the running total for the conversation.
		if l.Cost > s.costUSD {
			s.costUSD = l.Cost
		}
		s.turns += l.NumTurns
		finished := l.Subtype == "" || l.Subtype == "success"
		if !finished {
			// A turn that was cut short takes its requests with it. After one
			// that ran to its end, a request still waiting belongs to work
			// going on in the background and stays.
			s.pending = nil
			text := "Lượt này kết thúc sớm (" + clean.Text(l.Subtype, 60) + ")."
			if r := clean.Block(l.Result, 600); r != "" {
				text += "\n" + r
			}
			kind := KindNotice
			if l.Subtype != "error_during_execution" {
				kind = KindError
			}
			s.add(Item{Kind: kind, Text: text}, now)
		}
		s.state = StateIdle
		if len(s.pending) > 0 {
			s.state = StateWaiting
		}
		s.setActivity(ActivityNone, "")
		s.touch()
		return true
	}
	return false
}

// sameModel reports whether the model Claude Code says it runs is the one that
// was asked for. What it reports may carry more than the name asked for: a
// snapshot date after it, a context-window mark in brackets.
func sameModel(wanted, reported string) bool {
	plain := func(s string) string {
		if i := strings.IndexByte(s, '['); i >= 0 {
			s = s[:i]
		}
		return strings.ToLower(strings.TrimSpace(s))
	}
	w, r := plain(wanted), plain(reported)
	return w == r || strings.HasPrefix(r, w+"-") || strings.HasPrefix(w, r+"-")
}

// onControlRequest handles a request from Claude Code that expects an answer.
// Only one kind is understood: may this tool be used with this input. It is
// put to the user when it can be shown in full; in every other case the
// answer is no, given at once, so that nothing is ever allowed that the user
// did not see.
func (s *session) onControlRequest(l wireLine, now time.Time) bool {
	if l.RequestID == "" {
		return false
	}
	var req wireRequest
	_ = json.Unmarshal(l.Request, &req)
	if req.Subtype != "can_use_tool" {
		// Something this program does not implement. Saying so lets Claude
		// Code carry on instead of waiting for an answer.
		s.outbox = append(s.outbox, controlError(l.RequestID, "not supported by this host"))
		return false
	}

	refuse := func(why string) bool {
		s.outbox = append(s.outbox, permissionResponse(l.RequestID, false, nil))
		s.add(Item{Kind: KindError, Text: "App đã tự từ chối một yêu cầu xin phép vì " + why + "."}, now)
		return true
	}
	tool := strings.TrimSpace(req.ToolName)
	switch {
	case tool == "":
		return refuse("nó không nói rõ tool nào")
	case !isObject(req.Input):
		return refuse("không đọc được nội dung của nó (tool " + clean.Text(tool, 80) + ")")
	case s.pendingIndex(l.RequestID) >= 0:
		return refuse("nó dùng lại mã của một yêu cầu đang chờ")
	case len(s.pending) >= maxPending:
		return refuse("đang có quá nhiều yêu cầu chờ duyệt")
	}

	p := approvalOf(tool, req.Input)
	p.RequestID = l.RequestID
	p.AtMs = now.UnixMilli()
	p.rawInput = req.Input
	p.toolUseID = req.ToolUseID
	s.pending = append(s.pending, p)
	s.state = StateWaiting
	s.touch()
	return true
}

// onStreamEvent follows text as it is written, so a long answer appears as it
// comes rather than all at once, and notes what the model has started on.
func (s *session) onStreamEvent(raw json.RawMessage, now time.Time) bool {
	var e wireEvent
	if json.Unmarshal(raw, &e) != nil {
		return false
	}
	switch e.Type {
	case "content_block_start":
		switch e.ContentBlock.Type {
		case "thinking", "redacted_thinking":
			return s.setActivity(ActivityThinking, "")
		case "tool_use":
			// The model is writing the call; the tool has not started.
			return s.setActivity(ActivityWriting, clean.Text(e.ContentBlock.Name, 80))
		case "text":
			s.endLive()
			it := s.add(Item{Kind: KindText, Live: true}, now)
			s.liveSeq = it.Seq
			s.live.Reset()
			s.setActivity(ActivityWriting, "")
			return true
		}
		return false
	case "content_block_delta":
		if e.Delta.Type != "text_delta" || e.Delta.Text == "" {
			return false
		}
		it := s.find(s.liveSeq)
		if it == nil {
			it = s.add(Item{Kind: KindText, Live: true}, now)
			s.liveSeq = it.Seq
			s.live.Reset()
		}
		// Only what still fits is kept: one oversized piece must not be
		// stored whole.
		if room := maxTextRunes*4 - s.live.Len(); room > 0 {
			piece := e.Delta.Text
			if len(piece) > room {
				piece = strings.ToValidUTF8(piece[:room], "")
			}
			s.live.WriteString(piece)
			it.Text = s.live.String()
		}
		s.rev++
		it.Rev = s.rev
		return true
	}
	return false
}

// finishText records a completed piece of the agent's answer, replacing the
// copy that was being streamed if there is one.
func (s *session) finishText(text string, now time.Time) {
	if it := s.find(s.liveSeq); it != nil {
		it.Text, it.Live = text, false
		s.rev++
		it.Rev = s.rev
		s.liveSeq = 0
		s.live.Reset()
		return
	}
	if text != "" {
		s.add(Item{Kind: KindText, Text: text}, now)
	}
}

// endLive closes a streamed text item that never got its final form.
func (s *session) endLive() {
	if it := s.find(s.liveSeq); it != nil {
		it.Live = false
		it.Text = clean.Block(it.Text, maxTextRunes)
		s.rev++
		it.Rev = s.rev
	}
	s.liveSeq = 0
	s.live.Reset()
}

func resultText(raw json.RawMessage) string {
	var parts []string
	for _, b := range blocksOf(raw) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ---- showing a tool's input ----------------------------------------------------

// fieldSpec names an input field a reader wants first, and how to label it.
type fieldSpec struct {
	key, label string
	claim      bool
}

// leading lists those fields for the tools this program knows. It only
// decides order and wording: every other field of an input is shown too,
// after these, under its own name.
var leading = map[string][]fieldSpec{
	"Bash":         {{"command", "Lệnh sẽ chạy", false}, {"description", "Agent mô tả lệnh này là", true}},
	"PowerShell":   {{"command", "Lệnh sẽ chạy", false}, {"description", "Agent mô tả lệnh này là", true}},
	"Write":        {{"file_path", "File", false}, {"content", "Nội dung sẽ ghi", false}},
	"Edit":         {{"file_path", "File", false}, {"old_string", "Đoạn bị thay", false}, {"new_string", "Đoạn thay vào", false}},
	"NotebookEdit": {{"notebook_path", "Notebook", false}, {"new_source", "Nội dung ô mới", false}},
	"Read":         {{"file_path", "File", false}},
	"Grep":         {{"pattern", "Mẫu tìm", false}, {"path", "Tìm trong", false}},
	"Glob":         {{"pattern", "Mẫu tìm", false}, {"path", "Tìm trong", false}},
	"WebFetch":     {{"url", "Địa chỉ", false}, {"prompt", "Yêu cầu với trang này", false}},
	"WebSearch":    {{"query", "Tìm trên web", false}},
	"Agent":        {{"prompt", "Việc giao cho agent con", false}, {"description", "Agent mô tả việc này là", true}},
	"Task":         {{"prompt", "Việc giao cho agent con", false}, {"description", "Agent mô tả việc này là", true}},
}

// cutRunes returns at most n runes of s and how many runes were left off.
func cutRunes(s string, n int) (head string, rest int) {
	if n <= 0 {
		return "", utf8.RuneCountInString(s)
	}
	i, count := 0, 0
	for i < len(s) && count < n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	return s[:i], utf8.RuneCountInString(s[i:])
}

// approvalOf lays a tool's input out for the card on which the user allows or
// denies it. The rule it keeps: what is approved has been shown. Every field
// of the input is on the card, the ones this program does not know included;
// what the agent says about the action is labelled as its claim; characters
// that cannot be seen are replaced by visible marks; and if the input is too
// long to show whole, the card says how much is missing. raw must be a JSON
// object.
func approvalOf(tool string, raw json.RawMessage) Permission {
	p := Permission{Tool: clean.Text(tool, 80), Fields: []InputField{}}

	// A file edit that is understood completely reads best as a diff, and the
	// diff holds every line of it.
	if c, marked, ok := changeOf(tool, raw, true); ok {
		p.Change, p.Marked = &c, marked
		return p
	}

	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	order := make([]fieldSpec, 0, len(fields))
	placed := map[string]bool{}
	for _, spec := range leading[tool] {
		if _, ok := fields[spec.key]; ok {
			order = append(order, spec)
			placed[spec.key] = true
		}
	}
	rest := make([]string, 0, len(fields))
	for key := range fields {
		if !placed[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range rest {
		order = append(order, fieldSpec{key: key})
	}

	room := maxPermitRunes
	for _, spec := range order {
		value := fieldText(fields[spec.key])
		label := spec.label
		if label == "" {
			// A field this program has no name for is shown under its own.
			shown, marked := clean.Reveal(spec.key)
			label, _ = cutRunes(shown, 80)
			p.Marked += marked
		}
		head, cut := cutRunes(value, room)
		p.Hidden += cut
		shown, marked := clean.Reveal(head)
		p.Marked += marked
		room -= utf8.RuneCountInString(head)
		p.Fields = append(p.Fields, InputField{Label: label, Value: shown, Claim: spec.claim})
	}
	return p
}

// fieldText is a field's value as text: a string as it is, anything else as
// the JSON it was sent as.
func fieldText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		return pretty.String()
	}
	return string(raw)
}

// describeInput renders a tool's input for the conversation, where it is a
// record of what was done: the telling fields, cut when long.
func describeInput(tool string, raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &fields) != nil {
		return clean.Block(string(raw), maxInputRunes)
	}
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(fields[key], &s)
		return s
	}
	var out string
	switch tool {
	case "Bash", "PowerShell":
		// The command comes first: it is what ran. What the agent said about
		// it follows, marked as the agent's words.
		out = str("command")
		if d := str("description"); d != "" {
			out += "\n\n# agent mô tả: " + d
		}
	case "Write":
		out = str("file_path") + "\n\n" + str("content")
	case "Edit":
		out = str("file_path") + "\n\n--- thay\n" + str("old_string") + "\n\n+++ bằng\n" + str("new_string")
	case "Read", "NotebookEdit":
		out = str("file_path")
		if out == "" {
			out = str("notebook_path")
		}
	case "Grep", "Glob":
		out = str("pattern")
		if p := str("path"); p != "" {
			out += "\ntrong " + p
		}
	case "WebFetch":
		out = str("url")
	case "WebSearch":
		out = str("query")
	case "Agent", "Task":
		out = str("description")
		if p := str("prompt"); p != "" {
			out += "\n\n" + p
		}
	}
	if strings.TrimSpace(out) == "" {
		// An unfamiliar tool: show its input as it is.
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "", "  ") == nil {
			out = pretty.String()
		} else {
			out = string(raw)
		}
	}
	return clean.Block(out, maxInputRunes)
}
