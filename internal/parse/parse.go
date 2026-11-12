// Package parse reads Claude Code transcript files (JSON Lines) incrementally
// and keeps running aggregates per file: token usage, the size of the context
// at the last response, what the agent was last doing, and when.
//
// The transcript format is internal to Claude Code and changes between
// versions, so every field is optional and unknown line types are ignored.
package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"monitor-agent/internal/clean"
	"monitor-agent/internal/fsx"
	"monitor-agent/internal/usage"
)

const (
	// clipRunes bounds the snippets kept for display.
	clipRunes = 180
	// labelRunes bounds short labels: tool names, model ids, branch names.
	labelRunes = 80
	// pulseKeepMinutes is how much per-minute activity history is retained.
	pulseKeepMinutes = 180
	readBufferSize   = 256 << 10
	// recentWrite is how long after its last write a file is still checked
	// through an open handle rather than by its directory entry, which
	// Windows may update late for a file another process holds open.
	recentWrite = 15 * time.Minute
)

// maxLineBytes bounds one transcript line. A longer line is skipped rather
// than buffered whole; real lines are far below this. It is a variable so a
// test can lower it.
var maxLineBytes = 64 << 20

// Key separates usage by the dimensions that change its price or the period
// it is reported under.
type Key struct {
	Day   string // local calendar date, 2006-01-02; empty when the line had no timestamp
	Model string
	Fast  bool
}

// Activity is the most recent thing an agent did.
type Activity struct {
	Kind   string // "tool", "text", "thinking", or "result" for a subagent's final report
	Tool   string
	Detail string
	IsPath bool
	At     time.Time
}

type counted struct {
	key    Key
	tokens usage.Tokens
}

// File is the incremental state of one transcript. Create it with NewFile. It
// is not safe for concurrent use.
type File struct {
	Path string

	// Usage is everything the file's responses consumed. Each API response is
	// counted once even though the transcript repeats it on several lines.
	Usage map[Key]usage.Tokens
	// ContextTokens is the size of the last response's context: its input,
	// cache and output tokens. This is the figure Claude Code shows per agent.
	ContextTokens int64
	Model         string
	ToolUses      int
	FirstAt       time.Time
	LastAt        time.Time
	// ModTime is the file's modification time at the last successful read.
	ModTime    time.Time
	Title      string
	LastPrompt string
	GitBranch  string
	// Cwd is the working directory the session ran in, as its lines record
	// it. It is how a session that has ended is put back under its project.
	Cwd  string
	Last Activity
	// RecordedCostUSD is the last cost Claude Code wrote for the session
	// itself, when the transcript has one. It is kept for cross-checking.
	RecordedCostUSD float64
	HasRecordedCost bool
	// BadLines counts lines that were not JSON or were too long to read;
	// BadUsage counts responses whose usage record could not be understood
	// and was therefore left out of the totals.
	BadLines int
	BadUsage int
	// TurnEnded is true when the transcript ends with the agent finishing:
	// it handed its report back, or its last response ended the turn. It
	// turns false again if the agent is asked to continue.
	TurnEnded bool
	// AwaitingTool is true while the last thing in the transcript is a tool
	// call whose result has not arrived: the agent is waiting, not idle.
	AwaitingTool bool

	loc             *time.Location
	acceptSidechain bool
	offset          int64
	seenSize        int64
	ident           os.FileInfo
	lineNo          int64
	seen            map[string]counted
	pulse           map[int64]int
	pulseMax        int64
	// version goes up every time the state changes: when lines are taken in,
	// and when the file is found replaced and read again from the start.
	version int64
}

// NewFile prepares to read path. loc decides which calendar day a response
// belongs to. acceptSidechain is true for subagent transcripts, whose lines
// are all marked as side chains; in a main transcript such lines are counted
// for usage but do not describe the main thread.
func NewFile(path string, loc *time.Location, acceptSidechain bool) *File {
	if loc == nil {
		loc = time.Local
	}
	f := &File{Path: path, loc: loc, acceptSidechain: acceptSidechain}
	f.reset()
	return f
}

func (f *File) reset() {
	f.version++
	f.Usage = map[Key]usage.Tokens{}
	f.seen = map[string]counted{}
	f.pulse = map[int64]int{}
	f.pulseMax = 0
	f.offset = 0
	f.seenSize = 0
	f.ident = nil
	f.lineNo = 0
	f.ContextTokens = 0
	f.Model = ""
	f.ToolUses = 0
	f.FirstAt = time.Time{}
	f.LastAt = time.Time{}
	f.ModTime = time.Time{}
	f.Title = ""
	f.LastPrompt = ""
	f.GitBranch = ""
	f.Cwd = ""
	f.Last = Activity{}
	f.RecordedCostUSD = 0
	f.HasRecordedCost = false
	f.BadLines = 0
	f.BadUsage = 0
	f.TurnEnded = false
	f.AwaitingTool = false
}

// Update reads whatever was appended since the previous call. A final line
// that the writer has not finished is left for the next call. If the file was
// replaced by a different one, it is read again from the start.
func (f *File) Update() error {
	if f.seen == nil {
		f.reset()
	}
	if f.loc == nil {
		f.loc = time.Local
	}

	// Cheap check first: a file that has not been written for a while and
	// whose size and time are unchanged needs no handle opened.
	if f.ident != nil && time.Since(f.ModTime) > recentWrite {
		if st, err := os.Stat(f.Path); err == nil && st.Size() == f.seenSize && st.ModTime().Equal(f.ModTime) {
			return nil
		}
	}

	fh, st, err := fsx.OpenRegular(f.Path)
	if err != nil {
		return err
	}
	defer fh.Close()

	// The file was replaced if it is a different file now, if it is shorter
	// than what was already read, or if the position reached last time is no
	// longer the end of a line.
	replaced := st.Size() < f.offset ||
		(f.ident != nil && !os.SameFile(f.ident, st)) ||
		(f.offset > 0 && !newlineAt(fh, f.offset-1))
	if replaced {
		f.reset()
	}
	// st comes from the open handle, so it identifies this exact file even
	// if the path is later pointed at another one.
	f.ident = st
	unchanged := st.Size() == f.seenSize && st.ModTime().Equal(f.ModTime)
	f.ModTime = st.ModTime()
	f.seenSize = st.Size()
	if st.Size() == f.offset || (unchanged && !replaced) {
		// Nothing new, or only the same unfinished last line as before.
		return nil
	}

	if _, err := fh.Seek(f.offset, io.SeekStart); err != nil {
		return err
	}
	size := int(min(int64(readBufferSize), st.Size()-f.offset))
	r := bufio.NewReaderSize(fh, max(size, 4096))
	var buf []byte
	for {
		line, n, oversize, complete, err := readLine(r, buf[:0])
		buf = line
		if err != nil {
			return err
		}
		if !complete {
			return nil
		}
		f.offset += n
		f.lineNo++
		f.version++
		if oversize {
			f.BadLines++
			continue
		}
		f.consume(bytes.TrimSpace(line))
	}
}

func newlineAt(r io.ReaderAt, off int64) bool {
	var b [1]byte
	_, err := r.ReadAt(b[:], off)
	return err == nil && b[0] == '\n'
}

// readLine reads up to and including the next newline, appending to buf. n is
// the number of bytes consumed. complete is false when the input ended before
// a newline: the writer is still on that line and nothing was consumed. A line
// longer than maxLineBytes is read past without being kept, and oversize is
// true.
func readLine(r *bufio.Reader, buf []byte) (line []byte, n int64, oversize, complete bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		n += int64(len(chunk))
		if !oversize {
			if len(buf)+len(chunk) > maxLineBytes {
				oversize = true
				buf = buf[:0]
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case err == nil:
			return buf, n, oversize, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return buf, 0, false, false, nil
		default:
			return buf, 0, false, false, err
		}
	}
}

type rawLine struct {
	Type         string          `json:"type"`
	Timestamp    string          `json:"timestamp"`
	UUID         string          `json:"uuid"`
	IsSidechain  bool            `json:"isSidechain"`
	GitBranch    string          `json:"gitBranch"`
	Cwd          string          `json:"cwd"`
	Message      json.RawMessage `json:"message"`
	AITitle      string          `json:"aiTitle"`
	LastPrompt   string          `json:"lastPrompt"`
	TotalCostUSD *float64        `json:"totalCostUSD"`
}

type rawMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
	Usage      json.RawMessage `json:"usage"`
}

type rawUsage struct {
	Input         int64  `json:"input_tokens"`
	Output        int64  `json:"output_tokens"`
	CacheCreation int64  `json:"cache_creation_input_tokens"`
	CacheRead     int64  `json:"cache_read_input_tokens"`
	Speed         string `json:"speed"`
	CacheSplit    *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServerToolUse *struct {
		WebSearchRequests int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
}

type rawBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Request logs are large and carry nothing this package needs; they are
// recognised by their leading key and skipped without being decoded.
var skipPrefixes = [][]byte{
	[]byte(`{"type":"api-request`),
	[]byte(`{"type":"file-history-snapshot"`),
}

// decode unmarshals JSON while tolerating fields whose type differs from what
// is expected: those are left at their zero value and the rest is kept. It is
// right for descriptive fields. Numbers that feed the totals are decoded
// strictly instead; see onAssistant.
func decode(data []byte, v any) bool {
	err := json.Unmarshal(data, v)
	if err == nil {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

func (f *File) consume(line []byte) {
	if len(line) == 0 {
		return
	}
	for _, p := range skipPrefixes {
		if bytes.HasPrefix(line, p) {
			return
		}
	}
	var rl rawLine
	if !decode(line, &rl) {
		f.BadLines++
		return
	}
	ts := parseTime(rl.Timestamp)
	if !ts.IsZero() {
		if f.FirstAt.IsZero() || ts.Before(f.FirstAt) {
			f.FirstAt = ts
		}
		if ts.After(f.LastAt) {
			f.LastAt = ts
		}
	}
	if rl.GitBranch != "" {
		f.GitBranch = clean.Text(rl.GitBranch, labelRunes)
	}
	if rl.Cwd != "" {
		// The latest one: a session can be moved to another folder.
		f.Cwd = rl.Cwd
	}
	switch rl.Type {
	case "assistant":
		f.touch(ts)
		f.onAssistant(rl, ts)
	case "user":
		f.touch(ts)
		f.onUser(rl)
	case "ai-title":
		if s := clean.Snippet(rl.AITitle, clipRunes); s != "" {
			f.Title = s
		}
	case "last-prompt":
		if s := clean.Snippet(rl.LastPrompt, clipRunes); s != "" {
			f.LastPrompt = s
		}
	case "cost-state":
		if rl.TotalCostUSD != nil {
			f.RecordedCostUSD = *rl.TotalCostUSD
			f.HasRecordedCost = true
		}
	}
}

// Tools a subagent calls to return its result and end its turn. Which one is
// used depends on the Claude Code version.
var terminalTools = map[string]bool{
	"SubagentHandback": true,
	"StructuredOutput": true,
}

func (f *File) onAssistant(rl rawLine, ts time.Time) {
	var m rawMessage
	if len(rl.Message) == 0 || !decode(rl.Message, &m) {
		return
	}
	mainThread := f.acceptSidechain || !rl.IsSidechain

	if mainThread {
		ended := m.StopReason == "end_turn"
		awaiting := false
		for _, b := range decodeBlocks(m.Content) {
			switch b.Type {
			case "tool_use":
				f.ToolUses++
				name := clean.Text(b.Name, labelRunes)
				if terminalTools[b.Name] {
					// The report is what is worth showing, not the tool's name.
					ended = true
					f.Last = Activity{Kind: "result", Detail: snippet(report(b.Input)), At: ts}
					continue
				}
				ended, awaiting = false, true
				detail, isPath := toolDetail(b.Input)
				f.Last = Activity{Kind: "tool", Tool: name, Detail: detail, IsPath: isPath, At: ts}
			case "text":
				if s := snippet(b.Text); s != "" {
					f.Last = Activity{Kind: "text", Detail: s, At: ts}
				}
			case "thinking":
				f.Last = Activity{Kind: "thinking", At: ts}
			}
		}
		f.TurnEnded, f.AwaitingTool = ended, awaiting
	}

	// Lines Claude Code writes for its own messages (errors, interruptions)
	// carry a placeholder model and no real usage.
	if len(m.Usage) == 0 || bytes.Equal(bytes.TrimSpace(m.Usage), []byte("null")) || m.Model == "" || strings.HasPrefix(m.Model, "<") {
		return
	}
	// Usage is decoded strictly. A lenient decode would turn a field of an
	// unexpected shape into zero and let it replace a count that was right.
	var u rawUsage
	if err := json.Unmarshal(m.Usage, &u); err != nil {
		f.BadUsage++
		return
	}
	model := clean.Text(m.Model, labelRunes)

	id := m.ID
	if id == "" {
		id = rl.UUID
	}
	if id == "" {
		id = "line-" + strconv.FormatInt(f.lineNo, 10)
	}
	tk := tokensOf(&u)
	key := Key{Day: f.day(ts), Model: model, Fast: u.Speed == "fast"}
	if prev, ok := f.seen[id]; ok {
		// The same response appears once per content block. Keep the day of
		// its first line so a response is never split across two days.
		key.Day = prev.key.Day
		if prev.key != key || prev.tokens != tk {
			f.addUsage(prev.key, prev.tokens, -1)
			f.addUsage(key, tk, 1)
			f.seen[id] = counted{key: key, tokens: tk}
		}
	} else {
		f.addUsage(key, tk, 1)
		f.seen[id] = counted{key: key, tokens: tk}
	}

	if mainThread {
		f.ContextTokens = nonNegative(u.Input) + nonNegative(u.CacheCreation) + nonNegative(u.CacheRead) + nonNegative(u.Output)
		f.Model = model
	}
}

// onUser handles a line written on the user's side of the conversation: a
// tool result, or a new prompt.
func (f *File) onUser(rl rawLine) {
	if !f.acceptSidechain && rl.IsSidechain {
		return
	}
	// Whatever was awaited has arrived, or was overtaken by a new prompt.
	f.AwaitingTool = false
	if isPrompt(rl.Message) {
		// Asked to continue: a finished agent is working again.
		f.TurnEnded = false
	}
}

// isPrompt reports whether a user message carries text typed or sent to the
// agent, as opposed to only the results of its tool calls.
func isPrompt(message json.RawMessage) bool {
	var m rawMessage
	if len(message) == 0 || !decode(message, &m) {
		return false
	}
	content := bytes.TrimSpace(m.Content)
	if len(content) == 0 {
		return false
	}
	if content[0] == '"' {
		return true
	}
	for _, b := range decodeBlocks(content) {
		if b.Type == "text" {
			return true
		}
	}
	return false
}

// Responses calls yield for every API response counted in this file, with the
// id Claude Code gave it.
func (f *File) Responses(yield func(id string, k Key, tk usage.Tokens)) {
	for id, c := range f.seen {
		yield(id, c.key, c.tokens)
	}
}

// HasResponse reports whether this file counted the response with that id.
func (f *File) HasResponse(id string) bool {
	_, ok := f.seen[id]
	return ok
}

func (f *File) addUsage(k Key, tk usage.Tokens, sign int) {
	cur := f.Usage[k]
	if sign < 0 {
		cur.Sub(tk)
	} else {
		cur.Add(tk)
	}
	if cur == (usage.Tokens{}) {
		delete(f.Usage, k)
		return
	}
	f.Usage[k] = cur
}

func nonNegative(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

func tokensOf(u *rawUsage) usage.Tokens {
	tk := usage.Tokens{
		Input:     nonNegative(u.Input),
		Output:    nonNegative(u.Output),
		CacheRead: nonNegative(u.CacheRead),
		Requests:  1,
	}
	var w5m, w1h int64
	if u.CacheSplit != nil {
		w5m, w1h = nonNegative(u.CacheSplit.Ephemeral5m), nonNegative(u.CacheSplit.Ephemeral1h)
	}
	// Anything the split does not account for is priced at the API's default
	// five-minute lifetime; that is also the case for transcripts written
	// before the split was recorded.
	if rest := nonNegative(u.CacheCreation) - w5m - w1h; rest > 0 {
		w5m += rest
	}
	tk.CacheWrite5m, tk.CacheWrite1h = w5m, w1h
	if u.ServerToolUse != nil {
		tk.WebSearches = nonNegative(u.ServerToolUse.WebSearchRequests)
	}
	return tk
}

func decodeBlocks(raw json.RawMessage) []rawBlock {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var blocks []rawBlock
	if !decode(raw, &blocks) {
		return nil
	}
	return blocks
}

// detailKeys lists the tool input fields worth showing, most telling first.
var detailKeys = []struct {
	name   string
	isPath bool
}{
	{"file_path", true},
	{"notebook_path", true},
	{"description", false},
	{"command", false},
	{"pattern", false},
	{"query", false},
	{"url", false},
	{"skill", false},
	{"prompt", false},
	{"path", true},
}

func toolDetail(input json.RawMessage) (string, bool) {
	var fields map[string]json.RawMessage
	if len(input) == 0 || json.Unmarshal(input, &fields) != nil {
		return "", false
	}
	for _, k := range detailKeys {
		v, ok := fields[k.name]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			continue
		}
		if k.isPath {
			// A path is shown as it is; masking would only make it wrong.
			s = clean.Text(s, clipRunes)
		} else {
			s = snippet(s)
		}
		if s != "" {
			return s, k.isPath
		}
	}
	return "", false
}

// report returns the text of a subagent's final result, whichever field the
// terminal tool carries it in. An unrecognised shape yields nothing, and the
// agent is simply shown as having returned a result.
func report(input json.RawMessage) string {
	var fields map[string]json.RawMessage
	if len(input) == 0 || json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, name := range []string{"message", "summary", "result", "report"} {
		var s string
		if json.Unmarshal(fields[name], &s) == nil && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

var (
	markdownHeading  = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	markdownEmphasis = strings.NewReplacer("**", "", "`", "")
)

// plain removes the Markdown markers that are noise in a one-line snippet:
// heading hashes, bold markers and code ticks.
func plain(s string) string {
	return markdownEmphasis.Replace(markdownHeading.ReplaceAllString(s, ""))
}

// snippet is how transcript prose reaches the screen: Markdown markers
// removed, credential-like values masked, then shortened to one line.
func snippet(s string) string {
	return clean.Snippet(plain(s), clipRunes)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (f *File) day(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.In(f.loc).Format("2006-01-02")
}

func (f *File) touch(ts time.Time) {
	if ts.IsZero() {
		return
	}
	minute := ts.Unix() / 60
	f.pulse[minute]++
	if minute > f.pulseMax {
		f.pulseMax = minute
	}
	if len(f.pulse) > 2*pulseKeepMinutes {
		for m := range f.pulse {
			if m < f.pulseMax-pulseKeepMinutes {
				delete(f.pulse, m)
			}
		}
	}
}

// Pulse returns the number of transcript events in each of the n minutes
// ending with now's minute, oldest first.
func (f *File) Pulse(now time.Time, n int) []int {
	out := make([]int, n)
	last := now.Unix() / 60
	for i := range n {
		out[i] = f.pulse[last-int64(n-1-i)]
	}
	return out
}

// Offset is how many bytes of the file have been consumed.
func (f *File) Offset() int64 { return f.offset }

// Version changes whenever what the file adds up to may have changed: after
// an Update that took in at least one line, and after the file was found
// replaced. Two calls that return the same value saw the same state, so
// anything worked out from it can be kept until the value moves.
func (f *File) Version() int64 { return f.version }
