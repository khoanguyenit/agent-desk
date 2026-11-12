// Package agent runs Claude Code sessions that belong to this app: it starts
// the `claude` program in a project folder, sends it what the user types,
// follows what it does, and relays its requests for permission.
//
// It talks to Claude Code the way the official Agent SDKs do, over the
// process's standard input and output as JSON lines. The message stream is
// documented; the control messages used for permission requests and for
// interrupting a turn are what the SDKs send but are not published as a
// protocol for other programs. They were checked against Claude Code 2.1.322.
// Anything unrecognised is ignored, and a permission request that cannot be
// understood is refused, never answered with "allow".
package agent

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"monitor-agent/internal/clean"
	"monitor-agent/internal/fsx"
)

// Session states.
const (
	StateStarting = "starting" // process launched, nothing heard yet
	StateWorking  = "working"  // a turn is in progress
	StateWaiting  = "waiting"  // a permission request is waiting for the user
	StateIdle     = "idle"     // turn finished, ready for the next message
	StateEnded    = "ended"    // process exited normally
	StateFailed   = "failed"   // process could not start or exited with an error
)

// What a session is doing within a turn, as far as its output tells.
const (
	ActivityNone     = ""
	ActivityWorking  = "working"  // the model has the turn; nothing more is known
	ActivityThinking = "thinking" // the model is reasoning
	ActivityWriting  = "writing"  // the model is writing its answer
	ActivityTool     = "tool"     // a tool is running; View.ActivityTool names it
)

// Item kinds.
const (
	KindUser   = "user"
	KindText   = "text"
	KindTool   = "tool"
	KindNotice = "notice"
	KindError  = "error"
)

const (
	maxItems      = 600
	maxPending    = 40
	maxLineBytes  = 64 << 20
	maxTextRunes  = 60_000
	maxInputRunes = 8_000
	// A request for permission is shown whole up to this size; see
	// approvalOf.
	maxPermitRunes = 200_000
	maxResultRune  = 4_000
	stderrKeep     = 4_000
)

// allowedModes maps the modes a session may be started in to the value given
// to Claude Code. The mode is always passed, "ask" included: left out, a
// default mode set in the project's or the user's settings would apply
// instead, and the session would not be in the mode the user chose. The modes
// that never ask ("dontAsk", "bypassPermissions") are deliberately not here.
var allowedModes = map[string]string{
	"":            "manual",
	"ask":         "manual",
	"acceptEdits": "acceptEdits",
	"plan":        "plan",
}

// reportedModes lists, for each mode, what Claude Code may report at start-up
// when it honoured the request. Anything else stops the session.
var reportedModes = map[string][]string{
	"ask":         {"default", "manual"},
	"acceptEdits": {"acceptEdits"},
	"plan":        {"plan"},
}

var errEnded = errors.New("session đã kết thúc")

// What may be handed to Claude Code on its command line. A model name is
// letters, digits and a few marks, and never begins like a flag; a session to
// carry on with is named by a UUID and by nothing looser, because Claude Code
// also accepts a session's display name there, and a name is not an identity.
var (
	modelPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\[\]-]{0,79}$`)
	resumePattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Options says how a session is started.
type Options struct {
	// Dir is the project folder, Prompt the first request, and Mode "ask"
	// (the agent asks before each action that changes something),
	// "acceptEdits" or "plan".
	Dir    string
	Prompt string
	Mode   string
	// Model asks Claude Code for a model. Empty leaves the choice to its own
	// settings.
	Model string
	// Resume is the id of a session that has ended, to carry on with. Empty
	// starts a new session.
	Resume string
}

// Item is one entry of a session's conversation as the app shows it.
type Item struct {
	Seq  int64  `json:"seq"`
	Rev  int64  `json:"rev"`
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Tool calls: the tool's name, its input as text, and once it has run,
	// its result. Change is set as well when the call edits a file.
	Tool     string  `json:"tool"`
	Input    string  `json:"input"`
	Change   *Change `json:"change"`
	Result   string  `json:"result"`
	Done     bool    `json:"done"`
	IsError  bool    `json:"isError"`
	Decision string  `json:"decision"` // "allowed" or "denied" when the user was asked
	// Live marks text that is still being written.
	Live bool  `json:"live"`
	AtMs int64 `json:"atMs"`

	toolUseID string
}

// InputField is one field of a tool's input, as shown for approval.
type InputField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Claim marks words the agent wrote about the action, as opposed to the
	// action itself. They are shown as its claim, not as fact.
	Claim bool `json:"claim"`
}

// Permission is a request from the agent to use a tool, waiting for the user.
type Permission struct {
	RequestID string `json:"requestId"`
	Tool      string `json:"tool"`
	// Fields is the whole input: the fields a reader wants first, then every
	// other one. When the input is a file edit that is understood completely,
	// Change holds it as a diff instead and Fields is empty.
	Fields []InputField `json:"fields"`
	Change *Change      `json:"change"`
	// Hidden is how many characters at the end of the input are not shown
	// because it is too long; zero when all of it is there.
	Hidden int `json:"hidden"`
	// Marked is how many invisible characters in the input were replaced by
	// visible marks.
	Marked int   `json:"marked"`
	AtMs   int64 `json:"atMs"`

	rawInput  json.RawMessage
	toolUseID string
	answering bool
}

// View is what the page needs to draw a session.
type View struct {
	ID           string  `json:"id"`
	Dir          string  `json:"dir"`
	Title        string  `json:"title"`
	State        string  `json:"state"`
	Activity     string  `json:"activity"`
	ActivityTool string  `json:"activityTool"`
	Model        string  `json:"model"`
	Mode         string  `json:"mode"`
	CostUSD      float64 `json:"costUsd"`
	Turns        int     `json:"turns"`
	StartedAtMs  int64   `json:"startedAtMs"`
	Problem      string  `json:"problem"`
	Rev          int64   `json:"rev"`
	// FirstSeq is the seq of the oldest item still kept. The page drops what
	// it holds from before it.
	FirstSeq int64        `json:"firstSeq"`
	Items    []Item       `json:"items"`
	Pending  []Permission `json:"pending"`
}

type session struct {
	mu           sync.Mutex
	id           string
	dir          string
	title        string
	state        string
	activity     string
	activityTool string
	model        string
	mode         string
	costUSD      float64
	turns        int
	startedAt    time.Time
	problem      string
	rev          int64
	seq          int64
	items        []Item
	pending      []Permission
	liveSeq      int64 // seq of the text item being streamed, or 0
	live         strings.Builder
	ctlSeq       int64
	outbox       []any // replies decided while reading, written once the lock is released
	abort        bool  // the session must not go on; see consume

	// wmu serialises writes to the process. It is separate from mu, and
	// nothing waits for it while holding mu: a process that has stopped
	// reading must not be able to freeze everything that needs the session's
	// state, the way out included.
	wmu   sync.Mutex
	stdin *os.File

	// wantModel is the model the user asked for, if any. claudeID is the id
	// Claude Code gave the session when that is not the one it was asked to
	// use.
	wantModel string
	claudeID  string

	cmd    *exec.Cmd
	group  *group
	exited chan struct{}
	stderr []byte
}

// Manager owns the sessions this app started.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	order    []string
	// opening holds the ids of sessions being opened again, so that the
	// same one cannot be opened twice at once.
	opening map[string]bool
	notify  func(sessionID string)
	find    func() (string, error)
	now     func() time.Time

	// endGrace is how long a session asked to end is given before its
	// processes are stopped; quitGrace is the same when the whole app is
	// closing. drainGrace is how long the last output of an exited process is
	// waited for, and haltGrace how long a running turn is given to take the
	// request to stop.
	endGrace, quitGrace, drainGrace, haltGrace time.Duration
}

// NewManager creates a Manager. notify is called, from any goroutine, each
// time a session changes; it must not block.
func NewManager(notify func(sessionID string)) *Manager {
	if notify == nil {
		notify = func(string) {}
	}
	return &Manager{
		sessions: map[string]*session{}, opening: map[string]bool{}, notify: notify, find: FindClaude, now: time.Now,
		endGrace: 8 * time.Second, quitGrace: 3 * time.Second, drainGrace: 2 * time.Second, haltGrace: time.Second,
	}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Start launches a new session in dir and sends it prompt. mode is "ask"
// (the agent asks before each action that changes something), "acceptEdits"
// or "plan". It returns the session's id.
func (m *Manager) Start(dir, prompt, mode string) (string, error) {
	return m.Open(Options{Dir: dir, Prompt: prompt, Mode: mode})
}

// Open launches a session and sends it its first request: a new session, or,
// with Options.Resume, one that has ended and is carried on under the id it
// had. It returns the session's id.
func (m *Manager) Open(o Options) (string, error) {
	dir, mode := o.Dir, o.Mode
	prompt := strings.TrimSpace(o.Prompt)
	if prompt == "" {
		return "", errors.New("chưa nhập yêu cầu")
	}
	flagMode, ok := allowedModes[mode]
	if !ok {
		return "", fmt.Errorf("chế độ quyền %q không được hỗ trợ", mode)
	}
	if mode == "" {
		mode = "ask"
	}
	if o.Model != "" && !modelPattern.MatchString(o.Model) {
		return "", errors.New("tên model không hợp lệ")
	}
	if o.Resume != "" && !resumePattern.MatchString(o.Resume) {
		return "", errors.New("mã của session cần mở lại không hợp lệ")
	}
	if err := fsx.LocalDir(dir); err != nil {
		return "", err
	}
	exe, err := m.find()
	if err != nil {
		return "", err
	}

	// A session carried on keeps its id. One this app still has a process
	// for cannot be opened a second time; one it has seen end hands its
	// conversation over to the new process.
	id := strings.ToLower(o.Resume)
	var before *session
	if id != "" {
		m.mu.Lock()
		before = m.sessions[id]
		busy := m.opening[id]
		if !busy && before != nil {
			before.mu.Lock()
			busy = !before.over()
			before.mu.Unlock()
		}
		if busy {
			m.mu.Unlock()
			return "", errors.New("session này đang mở trong app")
		}
		m.opening[id] = true
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			delete(m.opening, id)
			m.mu.Unlock()
		}()
	} else if id, err = newID(); err != nil {
		return "", err
	}

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		// Permission requests come to this program over the same stream.
		"--permission-prompt-tool", "stdio",
		"--permission-mode", flagMode,
	}
	if o.Resume != "" {
		args = append(args, "--resume", id)
	} else {
		args = append(args, "--session-id", id)
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = childEnv(os.Environ())
	hideWindow(cmd)

	// The pipes are this program's own rather than exec's. exec closes the
	// output pipes it makes as soon as the process exits, and whatever had
	// not been read yet, the last lines of a session, would be lost.
	var opened []*os.File
	closeAll := func() {
		for _, f := range opened {
			f.Close()
		}
	}
	pipe := func() (r, w *os.File, err error) {
		if r, w, err = os.Pipe(); err == nil {
			opened = append(opened, r, w)
		}
		return r, w, err
	}
	inR, inW, err := pipe()
	if err != nil {
		return "", err
	}
	outR, outW, err := pipe()
	if err != nil {
		closeAll()
		return "", err
	}
	errR, errW, err := pipe()
	if err != nil {
		closeAll()
		return "", err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW

	s := &session{
		id:        id,
		dir:       dir,
		title:     clean.Text(prompt, 80),
		state:     StateStarting,
		mode:      mode,
		wantModel: o.Model,
		startedAt: m.now(),
		cmd:       cmd,
		stdin:     inW,
		group:     newGroup(),
		exited:    make(chan struct{}),
	}
	if before != nil {
		// What was said before stays on screen, and the counters carry on
		// from where they were: the page asks for what changed after the
		// revision it holds.
		before.mu.Lock()
		s.items, s.seq, s.rev, s.title = slices.Clone(before.items), before.seq, before.rev, before.title
		before.mu.Unlock()
	}
	if o.Resume != "" {
		note := "Tiếp tục session đã đóng. Những gì diễn ra trước đó nằm trong transcript của Claude Code."
		if before != nil {
			note = "Đã mở lại session này."
		}
		s.add(Item{Kind: KindNotice, Text: note}, m.now())
	}
	if err := cmd.Start(); err != nil {
		closeAll()
		s.group.release()
		return "", fmt.Errorf("không chạy được Claude Code (%s): %w", exe, err)
	}
	s.group.add(cmd.Process)
	// The child has its own copies of its ends now.
	inR.Close()
	outW.Close()
	errW.Close()

	m.mu.Lock()
	if _, listed := m.sessions[id]; !listed {
		m.order = append(m.order, id)
	}
	m.sessions[id] = s
	m.mu.Unlock()

	read := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		m.readOutput(s, outR)
	}()
	go func() {
		defer readers.Done()
		s.readErrors(errR)
	}()
	go func() {
		readers.Wait()
		close(read)
	}()
	go m.wait(s, read, outR, errR)

	if err := m.Send(id, prompt); err != nil {
		return id, err
	}
	return id, nil
}

// childEnv is the environment for a Claude Code child process: this
// process's own, minus what would tie the child to the program this one was
// started from. CLAUDECODE makes Claude Code believe it runs inside another
// Claude Code session; the others would attach it to the editor whose
// terminal this app happened to be launched from.
func childEnv(env []string) []string {
	drop := []string{"CLAUDECODE=", "CLAUDE_CODE_ENTRYPOINT=", "CLAUDE_CODE_SSE_PORT=", "ENABLE_IDE_INTEGRATION="}
	out := make([]string, 0, len(env))
next:
	for _, kv := range env {
		for _, d := range drop {
			if len(kv) >= len(d) && strings.EqualFold(kv[:len(d)], d) {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		return s, nil
	}
	// Claude Code may run a session under another id than the one it was
	// asked to use. The board knows the session by that id.
	for _, s := range m.sessions {
		s.mu.Lock()
		same := s.claudeID != "" && s.claudeID == id
		s.mu.Unlock()
		if same {
			return s, nil
		}
	}
	return nil, errors.New("không tìm thấy session này")
}

// Owns reports whether id is a session this app started.
func (m *Manager) Owns(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[id]
	return ok
}

// State returns the state of a session, or "" when the app has no such one.
func (m *Manager) State(id string) string {
	s, err := m.get(id)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// send writes one JSON line to the process. The caller must not hold s.mu.
func (s *session) send(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	if w == nil {
		return errEnded
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = w.Write(append(raw, '\n'))
	return err
}

func (s *session) over() bool { return s.state == StateEnded || s.state == StateFailed }

// Send gives the session another message from the user. A message sent while
// a turn is running is queued by Claude Code and handled after it.
func (m *Manager) Send(id, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("chưa nhập yêu cầu")
	}
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.over() || s.stdin == nil {
		s.mu.Unlock()
		return errEnded
	}
	// The message is shown before it is written: what the agent answers must
	// never appear above the words it answers.
	s.add(Item{Kind: KindUser, Text: clean.Block(text, maxTextRunes)}, m.now())
	was := s.state
	if s.state != StateWaiting {
		s.state = StateWorking
		s.setActivity(ActivityWorking, "")
	}
	s.mu.Unlock()
	m.notify(id)

	if err := s.send(userMessage(text)); err != nil {
		s.mu.Lock()
		if !s.over() {
			s.add(Item{Kind: KindError, Text: "Không gửi được yêu cầu này cho Claude Code: " + err.Error()}, m.now())
			if s.state == StateWorking && was != StateWorking {
				s.state = was
				s.setActivity(ActivityNone, "")
			}
		}
		s.mu.Unlock()
		m.notify(id)
		return err
	}
	return nil
}

func (s *session) pendingIndex(requestID string) int {
	return slices.IndexFunc(s.pending, func(p Permission) bool { return p.RequestID == requestID })
}

// Decide answers a pending permission request.
func (m *Manager) Decide(id, requestID string, allow bool) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	idx := s.pendingIndex(requestID)
	if idx < 0 {
		s.mu.Unlock()
		return errors.New("yêu cầu xin quyền này không còn chờ nữa")
	}
	if s.pending[idx].answering {
		s.mu.Unlock()
		return errors.New("câu trả lời cho yêu cầu này đang được gửi")
	}
	s.pending[idx].answering = true
	raw, toolUseID := s.pending[idx].rawInput, s.pending[idx].toolUseID
	s.mu.Unlock()

	err = s.send(permissionResponse(requestID, allow, raw))

	s.mu.Lock()
	idx = s.pendingIndex(requestID)
	if err != nil {
		if idx >= 0 {
			s.pending[idx].answering = false
		}
		s.mu.Unlock()
		return err
	}
	if idx >= 0 {
		s.pending = slices.Delete(s.pending, idx, idx+1)
	}
	decision := "denied"
	if allow {
		decision = "allowed"
	}
	s.markDecision(toolUseID, decision)
	if len(s.pending) == 0 && s.state == StateWaiting {
		s.state = StateWorking
	}
	s.touch()
	s.mu.Unlock()
	m.notify(id)
	return nil
}

// Interrupt stops the turn in progress. The session stays open.
func (m *Manager) Interrupt(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ctlSeq++
	n := s.ctlSeq
	s.mu.Unlock()
	return s.send(interruptRequest(fmt.Sprintf("int-%d", n)))
}

// End closes the session: a turn in progress is told to stop, Claude Code is
// told there is no more input and given a moment to finish; then its
// processes are stopped.
func (m *Manager) End(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	m.stop(s, m.endGrace)
	return nil
}

func (m *Manager) stop(s *session, grace time.Duration) {
	s.mu.Lock()
	w := s.stdin
	s.stdin = nil
	busy := s.state == StateStarting || s.state == StateWorking || s.state == StateWaiting
	s.ctlSeq++
	n := s.ctlSeq
	s.mu.Unlock()

	if w != nil {
		if busy {
			// Without this a turn in progress would run on to its end with
			// nobody left to see it or to answer its requests.
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				raw, _ := json.Marshal(interruptRequest(fmt.Sprintf("int-%d", n)))
				s.wmu.Lock()
				_, _ = w.Write(append(raw, '\n'))
				s.wmu.Unlock()
			}()
			select {
			case <-sent:
			case <-time.After(m.haltGrace):
			}
		}
		// Closing also frees a write that is stuck on a process that has
		// stopped reading.
		_ = w.Close()
	}
	select {
	case <-s.exited:
	case <-time.After(grace):
		s.group.kill()
	}
}

// Shutdown ends every session. It is called when the app closes.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	all := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.stop(s, m.quitGrace)
		}()
	}
	wg.Wait()
}

// Forget drops a finished session from the list.
func (m *Manager) Forget(id string) error {
	s, err := m.get(id)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	over := s.over()
	s.mu.Unlock()
	if !over {
		return errors.New("session còn đang chạy")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	if i := slices.Index(m.order, id); i >= 0 {
		m.order = slices.Delete(m.order, i, i+1)
	}
	return nil
}

// List returns every session without its conversation, oldest first.
func (m *Manager) List() []View {
	m.mu.Lock()
	sessions := make([]*session, 0, len(m.order))
	for _, id := range m.order {
		sessions = append(sessions, m.sessions[id])
	}
	m.mu.Unlock()
	out := make([]View, 0, len(sessions))
	for _, s := range sessions {
		s.mu.Lock()
		out = append(out, s.view())
		s.mu.Unlock()
	}
	return out
}

// Get returns a session with the items that changed after revision afterRev.
// Pass 0 for everything.
func (m *Manager) Get(id string, afterRev int64) (View, error) {
	s, err := m.get(id)
	if err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.view()
	for _, it := range s.items {
		if it.Rev > afterRev {
			v.Items = append(v.Items, it)
		}
	}
	return v, nil
}

// view is the session without its items. The caller holds s.mu.
func (s *session) view() View {
	v := View{
		ID:           s.id,
		Dir:          s.dir,
		Title:        s.title,
		State:        s.state,
		Activity:     s.activity,
		ActivityTool: s.activityTool,
		Model:        s.model,
		Mode:         s.mode,
		CostUSD:      s.costUSD,
		Turns:        s.turns,
		StartedAtMs:  s.startedAt.UnixMilli(),
		Problem:      s.problem,
		Rev:          s.rev,
		Items:        []Item{},
		Pending:      append([]Permission{}, s.pending...),
	}
	if len(s.items) > 0 {
		v.FirstSeq = s.items[0].Seq
	}
	return v
}

// touch marks the session as changed. The caller holds s.mu.
func (s *session) touch() { s.rev++ }

// setActivity records what the session is doing and reports whether that is
// news. The caller holds s.mu.
func (s *session) setActivity(activity, tool string) bool {
	if s.activity == activity && s.activityTool == tool {
		return false
	}
	s.activity, s.activityTool = activity, tool
	s.rev++
	return true
}

// add appends an item. The caller holds s.mu.
func (s *session) add(it Item, now time.Time) *Item {
	s.seq++
	s.rev++
	it.Seq, it.Rev, it.AtMs = s.seq, s.rev, now.UnixMilli()
	s.items = append(s.items, it)
	if len(s.items) > maxItems {
		// The oldest go; the page learns of it from View.FirstSeq.
		s.items = append([]Item(nil), s.items[len(s.items)-maxItems:]...)
	}
	return &s.items[len(s.items)-1]
}

// find returns the item with the given seq. The caller holds s.mu.
func (s *session) find(seq int64) *Item {
	if seq == 0 {
		return nil
	}
	for i := len(s.items) - 1; i >= 0; i-- {
		if s.items[i].Seq == seq {
			return &s.items[i]
		}
	}
	return nil
}

func (s *session) findTool(toolUseID string) *Item {
	if toolUseID == "" {
		return nil
	}
	for i := len(s.items) - 1; i >= 0; i-- {
		if s.items[i].toolUseID == toolUseID {
			return &s.items[i]
		}
	}
	return nil
}

func (s *session) markDecision(toolUseID, decision string) {
	if it := s.findTool(toolUseID); it != nil {
		it.Decision = decision
		s.rev++
		it.Rev = s.rev
	}
}

func (m *Manager) readOutput(s *session, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256<<10), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		s.mu.Lock()
		changed := s.consume(line, m.now())
		replies := s.outbox
		s.outbox = nil
		abort := s.abort
		s.mu.Unlock()

		for _, v := range replies {
			_ = s.send(v)
		}
		if abort {
			s.group.kill()
		}
		if changed {
			m.notify(s.id)
		}
	}
	if err := sc.Err(); err != nil {
		select {
		case <-s.exited:
			// The session is over and its pipe was closed under the reader.
			return
		default:
		}
		s.mu.Lock()
		s.problem = "Mất kết nối với Claude Code: " + err.Error()
		s.touch()
		s.mu.Unlock()
		// Reading cannot continue, so the process is of no use any more.
		s.group.kill()
		m.notify(s.id)
	}
}

func (s *session) readErrors(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.stderr = append(s.stderr, buf[:n]...)
			if len(s.stderr) > stderrKeep {
				s.stderr = s.stderr[len(s.stderr)-stderrKeep:]
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// wait follows the process to its end. read is closed once everything the
// process wrote has been taken in.
func (m *Manager) wait(s *session, read <-chan struct{}, pipes ...io.Closer) {
	err := s.cmd.Wait()
	// What the process wrote last is read before the session is declared
	// over. A program it started may still hold the pipe open, though, and
	// that one is not waited for.
	select {
	case <-read:
	case <-time.After(m.drainGrace):
	}
	s.mu.Lock()
	closing := s.stdin == nil
	stdin := s.stdin
	s.stdin = nil
	s.pending = nil
	s.endLive()
	s.setActivity(ActivityNone, "")
	switch {
	case s.abort:
		// consume has already said why.
		s.state = StateFailed
	case err == nil || closing:
		s.state = StateEnded
	default:
		s.state = StateFailed
		if s.problem == "" {
			s.problem = "Claude Code dừng bất thường: " + err.Error()
			if detail := clean.Block(string(s.stderr), 600); detail != "" {
				s.problem += "\n" + detail
			}
		}
	}
	s.touch()
	s.mu.Unlock()

	close(s.exited)
	if stdin != nil {
		_ = stdin.Close()
	}
	for _, p := range pipes {
		_ = p.Close()
	}
	// Whatever the process left running goes with it.
	s.group.release()
	m.notify(s.id)
}
