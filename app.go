package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"monitor-agent/internal/agent"
	"monitor-agent/internal/clean"
	"monitor-agent/internal/fsx"
	"monitor-agent/internal/monitor"
	"monitor-agent/internal/projects"
)

const (
	// refreshEvery is how often the machine is scanned. The scan runs on its
	// own timer rather than on the page's, so "finished" notifications still
	// arrive while the window is minimised.
	refreshEvery = 2 * time.Second
	// chatEventGap bounds how often the page is told that a conversation
	// changed. Text streams in many small pieces; the page does not need to
	// redraw for each one.
	chatEventGap = 80 * time.Millisecond
	// chatEvent is the name of the event the page listens to.
	chatEvent = "chat"
)

const lockedMessage = "Bản dev đang khoá việc mở session. Xem ghi chú trên trang."

// App is the object bound to the frontend. Every exported method becomes a
// function the page can call.
type App struct {
	svc      *monitor.Service
	agents   *agent.Manager
	projects *projects.Store
	// notice is a standing warning added to every snapshot (see mode_dev.go).
	notice string
	// locked is set in a development build that was not explicitly opened
	// up: no project list is kept and no session can be started.
	locked bool

	ready     chan struct{} // closed once the first snapshot exists
	readyOnce sync.Once
	initMu    sync.Mutex  // serialises setting up notifications
	refreshMu sync.Mutex  // one scan at a time, so an older one cannot overwrite a newer
	asking    atomic.Bool // a folder or trust dialog is open

	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	closing     bool
	latest      monitor.Snapshot
	have        bool
	notify      bool
	privacy     bool
	notifyReady bool
	problem     string // why the last notification could not be sent
	listProblem string // why the saved project list could not be read
	sent        int
	chatTimers  map[string]bool   // sessions with a chat event already scheduled
	chatStates  map[string]string // last state seen per session, to spot a new wait
}

// NewApp creates the application with the real environment.
func NewApp() *App {
	opts, notice, locked := monitorOptions()
	a := &App{
		svc:        monitor.New(opts),
		notice:     notice,
		locked:     locked,
		ready:      make(chan struct{}),
		chatTimers: map[string]bool{},
		chatStates: map[string]string{},
	}
	a.agents = agent.NewManager(a.chatChanged)

	var listPath string
	if !locked {
		if dir, err := os.UserConfigDir(); err == nil {
			listPath = filepath.Join(dir, "monitor-agent", "projects.json")
		}
	}
	store, err := projects.Open(listPath)
	if err != nil {
		a.listProblem = "Không đọc được danh sách project đã thêm (" + clean.Text(err.Error(), 200) +
			"). Khi bạn thêm project mới, file cũ được giữ lại dưới tên projects.json.bak."
	}
	a.projects = store
	return a
}

// startup is called by Wails once the window exists.
func (a *App) startup(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.ctx, a.cancel = ctx, cancel
	a.mu.Unlock()
	go a.watch(loopCtx)
}

// shutdown is called by Wails when the window closes. Sessions the app
// started are ended with it: nobody would be left to answer their requests.
func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	cancel := a.cancel
	a.closing = true
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.agents.Shutdown()
}

func (a *App) watch(ctx context.Context) {
	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	for {
		a.refresh()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// decorate adds to a scan what only the app knows: the projects the user
// pinned, and which sessions are the app's own.
func (a *App) decorate(snap monitor.Snapshot) monitor.Snapshot {
	listed := map[string]int{}
	for i := range snap.Projects {
		listed[projects.Key(snap.Projects[i].Dir())] = i
		for j := range snap.Projects[i].Sessions {
			s := &snap.Projects[i].Sessions[j]
			state := a.agents.State(s.ID)
			s.Managed = state != ""
			s.AwaitingUser = state == agent.StateWaiting
		}
	}
	added := false
	for _, dir := range a.projects.List() {
		if i, ok := listed[projects.Key(dir)]; ok {
			snap.Projects[i].Pinned = true
			continue
		}
		snap.Projects = append(snap.Projects, monitor.PinnedProject(dir))
		added = true
	}
	if added {
		sort.SliceStable(snap.Projects, func(i, j int) bool {
			return projects.Key(snap.Projects[i].Dir()) < projects.Key(snap.Projects[j].Dir())
		})
		snap.Counts.Projects = len(snap.Projects)
	}
	return snap
}

// refresh takes a new snapshot, publishes it, and announces what finished
// since the previous one. It runs one at a time: the timer and the bound
// methods both call it, and two at once could publish their snapshots in the
// wrong order.
func (a *App) refresh() {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	// A bug in the scan must not take the whole app down with it; the page
	// keeps the last good snapshot and says what happened.
	defer func() {
		if r := recover(); r != nil {
			a.mu.Lock()
			a.problem = fmt.Sprintf("Lỗi nội bộ khi quét: %v", r)
			a.mu.Unlock()
			a.readyOnce.Do(func() { close(a.ready) })
		}
	}()

	snap := a.decorate(a.svc.Snapshot())

	a.mu.Lock()
	prev, had := a.latest, a.have
	a.latest, a.have = snap, true
	announce := had && a.notify && a.notifyReady
	privacy, ctx := a.privacy, a.ctx
	a.mu.Unlock()
	a.readyOnce.Do(func() { close(a.ready) })

	if !announce {
		return
	}
	for _, e := range monitor.Diff(prev, snap) {
		title, body := e.Text(privacy)
		a.send(ctx, title, body)
	}
}

func (a *App) send(ctx context.Context, title, body string) {
	a.mu.Lock()
	a.sent++
	id := "monitor-agent-" + strconv.Itoa(a.sent)
	a.mu.Unlock()

	err := runtime.SendNotification(ctx, runtime.NotificationOptions{ID: id, Title: title, Body: body})

	a.mu.Lock()
	if err != nil {
		a.problem = "Không gửi được thông báo: " + err.Error()
	} else {
		a.problem = ""
	}
	a.mu.Unlock()
}

// GetSnapshot returns what the page shows. The scan itself runs in the
// background; this hands out its latest result, waiting only for the very
// first one, which has to read every transcript in full.
func (a *App) GetSnapshot() monitor.Snapshot {
	select {
	case <-a.ready:
	case <-time.After(2 * time.Minute):
	}
	damaged := a.projects.Damaged()
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := a.latest
	// The snapshot is shared with later readers, so the warnings are copied
	// before any are added.
	snap.Warnings = append([]string{}, snap.Warnings...)
	if a.notice != "" {
		snap.Warnings = append(snap.Warnings, a.notice)
	}
	if a.listProblem != "" && damaged {
		snap.Warnings = append(snap.Warnings, a.listProblem)
	}
	if a.problem != "" {
		snap.Warnings = append(snap.Warnings, a.problem)
	}
	return snap
}

// SetOptions records the page's switches. notify turns desktop notifications
// on; announce asks for a confirmation toast, which the page requests when
// the user has just switched them on. privacy keeps session and agent names
// out of notifications. It returns an empty string, or what went wrong.
func (a *App) SetOptions(notify, privacy, announce bool) string {
	a.mu.Lock()
	a.notify, a.privacy = notify, privacy
	ctx := a.ctx
	a.mu.Unlock()
	if !notify || ctx == nil {
		return ""
	}

	a.initMu.Lock()
	defer a.initMu.Unlock()
	a.mu.Lock()
	ready := a.notifyReady
	a.mu.Unlock()
	if !ready {
		if err := runtime.InitializeNotifications(ctx); err != nil {
			return "Không bật được thông báo: " + err.Error()
		}
		// Clicking a notification brings the monitor forward.
		runtime.OnNotificationResponse(ctx, func(runtime.NotificationResult) {
			runtime.WindowUnminimise(ctx)
			runtime.WindowShow(ctx)
		})
		a.mu.Lock()
		a.notifyReady = true
		a.mu.Unlock()
	}
	if announce {
		a.send(ctx, "Agent Desk", "Đã bật thông báo. Bạn sẽ được báo khi một session hoặc agent làm xong.")
	}
	return ""
}

func (a *App) context() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctx
}

// project finds a project of the current snapshot by the path the page was
// given. Every method that acts on a folder goes through here, so the page
// can only ever name a folder the app already shows. The path the page holds
// is the one made for display, and two different folders can display alike;
// then neither is acted on, rather than one of them at a guess.
func (a *App) project(path string) (monitor.Project, string) {
	a.mu.Lock()
	snap := a.latest
	a.mu.Unlock()
	var found []monitor.Project
	for _, p := range snap.Projects {
		if p.Path == path {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return monitor.Project{}, "Project này không còn trong danh sách."
	case 1:
		return found[0], ""
	}
	return monitor.Project{}, "Có hai thư mục hiện cùng một đường dẫn trên bảng, nên app không chắc bạn chọn thư mục nào."
}

// OpenProject brings forward the VS Code window of a project that is on
// screen. It returns an empty string, or what went wrong.
func (a *App) OpenProject(path string) string {
	ctx := a.context()
	if ctx == nil {
		return "Ứng dụng chưa sẵn sàng."
	}
	p, problem := a.project(path)
	if problem != "" {
		return problem
	}
	if err := openURI(ctx, p.EditorURI()); err != nil {
		return "Không mở được VS Code: " + err.Error()
	}
	return ""
}

// The two addresses behind the credit line at the foot of the page. They are
// fixed here: the page names one of them and cannot have anything else opened.
const (
	creditSite    = "https://khoabug.com/"
	creditProfile = "https://www.linkedin.com/in/khoanguyenit/"
)

// OpenCredit opens the author's site or profile in the default browser. It
// returns an empty string, or what went wrong.
func (a *App) OpenCredit(which string) string {
	ctx := a.context()
	if ctx == nil {
		return "Ứng dụng chưa sẵn sàng."
	}
	var uri string
	switch which {
	case "site":
		uri = creditSite
	case "profile":
		uri = creditProfile
	default:
		return "Không có liên kết này."
	}
	if err := openURI(ctx, uri); err != nil {
		return "Không mở được trình duyệt: " + err.Error()
	}
	return ""
}

// confirmTrust asks the user whether sessions may be started in dir. Claude
// Code asks this itself in a terminal; started by a program it does not, so
// the question is put here. It is a native dialog, drawn by the app and not
// by the page, and "No" is the default.
func (a *App) confirmTrust(ctx context.Context, dir string) (bool, error) {
	answer, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:  runtime.QuestionDialog,
		Title: "Tin tưởng thư mục này?",
		Message: "Cho phép Agent Desk mở session Claude Code tại thư mục này?\n\n" + dir + "\n\n" +
			"Session mở từ app không hiện câu hỏi tin tưởng thư mục của Claude Code. " +
			"Hook, MCP server và các quy tắc cấp quyền khai báo trong thư mục này có hiệu lực ngay, " +
			"kể cả quy tắc cho chạy lệnh mà không hỏi.\n\n" +
			"Chỉ đồng ý với thư mục mà bạn tin tưởng.",
		Buttons:       []string{"Yes", "No"},
		DefaultButton: "No",
		CancelButton:  "No",
	})
	if err != nil {
		return false, err
	}
	return answer == "Yes", nil
}

// trust records dir as a folder sessions may be started in, after asking.
// It returns whether the folder is trusted now, and what went wrong if
// anything did; declining is not an error.
func (a *App) trust(dir string) (bool, string) {
	if a.locked {
		return false, lockedMessage
	}
	if a.projects.Has(dir) {
		return true, ""
	}
	ctx := a.context()
	if ctx == nil {
		return false, "Ứng dụng chưa sẵn sàng."
	}
	if err := fsx.LocalDir(dir); err != nil {
		return false, err.Error()
	}
	yes, err := a.confirmTrust(ctx, dir)
	if err != nil {
		return false, "Không hiện được hộp xác nhận: " + err.Error()
	}
	if !yes {
		return false, ""
	}
	if err := a.projects.Add(dir); err != nil {
		return false, "Không lưu được project: " + err.Error()
	}
	a.refresh()
	return true, ""
}

// AddProject lets the user pick a folder and adds it to the list of projects
// the app shows and may start sessions in. It returns an empty string, or
// what went wrong; cancelling a dialog is not an error.
func (a *App) AddProject() string {
	ctx := a.context()
	if ctx == nil {
		return "Ứng dụng chưa sẵn sàng."
	}
	if a.locked {
		return lockedMessage
	}
	// One dialog at a time: a second request while the first is open would
	// stack another dialog on top of it.
	if !a.asking.CompareAndSwap(false, true) {
		return ""
	}
	defer a.asking.Store(false)
	dir, err := runtime.OpenDirectoryDialog(ctx, runtime.OpenDialogOptions{Title: "Chọn thư mục project"})
	if err != nil {
		return "Không mở được hộp chọn thư mục: " + err.Error()
	}
	if dir == "" {
		return ""
	}
	_, problem := a.trust(dir)
	return problem
}

// TrustResult says whether sessions may be started in a project.
type TrustResult struct {
	OK      bool   `json:"ok"`
	Problem string `json:"problem"`
}

// TrustProject makes sure sessions may be started in a project that is on
// the board. A project the user added is trusted already. One that is on the
// board only because a session is running there has never been put to the
// user by this app, so the question is asked now, once; on "Yes" the project
// joins the added ones.
func (a *App) TrustProject(path string) TrustResult {
	p, problem := a.project(path)
	if problem != "" {
		return TrustResult{Problem: problem}
	}
	if a.projects.Has(p.Dir()) {
		return TrustResult{OK: true}
	}
	if !a.asking.CompareAndSwap(false, true) {
		return TrustResult{}
	}
	defer a.asking.Store(false)
	ok, problem := a.trust(p.Dir())
	return TrustResult{OK: ok, Problem: problem}
}

// RemoveProject takes a folder off the list of added projects. Sessions
// running there are not touched, and nothing on disk changes.
func (a *App) RemoveProject(path string) string {
	p, problem := a.project(path)
	if problem != "" {
		return ""
	}
	if err := a.projects.Remove(p.Dir()); err != nil {
		return "Không bỏ được project: " + err.Error()
	}
	a.refresh()
	return ""
}

// StartResult is the outcome of starting a session.
type StartResult struct {
	ID      string `json:"id"`
	Problem string `json:"problem"`
}

// StartSession opens a new Claude Code session in a project and sends it the
// user's first request. mode is "ask", "acceptEdits" or "plan". The project
// must be one the user has agreed to (see TrustProject): that is checked
// here, not left to the page.
func (a *App) StartSession(projectPath, prompt, mode string) StartResult {
	if a.locked {
		return StartResult{Problem: lockedMessage}
	}
	p, problem := a.project(projectPath)
	if problem != "" {
		return StartResult{Problem: problem}
	}
	if !a.projects.Has(p.Dir()) {
		return StartResult{Problem: "Thư mục này chưa được bạn xác nhận tin tưởng. Bấm \"Session mới\" lại để xác nhận."}
	}
	id, err := a.agents.Start(p.Dir(), prompt, mode)
	if err != nil {
		return StartResult{ID: id, Problem: err.Error()}
	}
	return StartResult{ID: id}
}

func problemOf(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// SendChat sends another message to a session the app started.
func (a *App) SendChat(id, text string) string {
	return problemOf(a.agents.Send(id, text))
}

// AnswerPermission allows or denies one pending request of a session.
func (a *App) AnswerPermission(id, requestID string, allow bool) string {
	return problemOf(a.agents.Decide(id, requestID, allow))
}

// InterruptSession stops the turn in progress and leaves the session open.
func (a *App) InterruptSession(id string) string {
	return problemOf(a.agents.Interrupt(id))
}

// EndSession closes a session the app started. It can take a few seconds:
// Claude Code is given the chance to finish what it is writing.
func (a *App) EndSession(id string) string {
	return problemOf(a.agents.End(id))
}

// ForgetSession removes a finished session from the list.
func (a *App) ForgetSession(id string) string {
	err := a.agents.Forget(id)
	if err == nil {
		a.mu.Lock()
		delete(a.chatStates, id)
		a.mu.Unlock()
	}
	return problemOf(err)
}

// ChatResult is a session's conversation, or why it could not be read.
type ChatResult struct {
	View    agent.View `json:"view"`
	Problem string     `json:"problem"`
}

// GetChat returns a session with the items that changed after afterRev.
func (a *App) GetChat(id string, afterRev int64) ChatResult {
	v, err := a.agents.Get(id, afterRev)
	if err != nil {
		return ChatResult{Problem: err.Error()}
	}
	// The page shows the model the way the board does: "Sonnet 5.5".
	v.Model = monitor.ModelLabel(v.Model)
	return ChatResult{View: v}
}

// ListChats returns the sessions the app started, without their items.
func (a *App) ListChats() []agent.View {
	return a.agents.List()
}

// ClaudeInfo is which Claude Code program sessions are started with.
type ClaudeInfo struct {
	Path    string `json:"path"`
	Problem string `json:"problem"`
}

// GetClaude tells the page which program a new session would run, so the
// user can see it before starting one.
func (a *App) GetClaude() ClaudeInfo {
	path, err := agent.FindClaude()
	if err != nil {
		return ClaudeInfo{Problem: err.Error()}
	}
	return ClaudeInfo{Path: path}
}

// TrailResult is the latest steps of an agent, or why they could not be read.
type TrailResult struct {
	Trail   monitor.Trail `json:"trail"`
	Problem string        `json:"problem"`
}

// GetTrail returns the latest steps of a running session's main thread, or
// of one of its agents when agentID is given. It is read-only, and reaches
// only sessions that are on the board. With "Ẩn nội dung" on, what was said
// and run is left out.
func (a *App) GetTrail(sessionID, agentID string) TrailResult {
	a.mu.Lock()
	hide := a.privacy
	a.mu.Unlock()
	tr, err := a.svc.Trail(sessionID, agentID, hide)
	if err != nil {
		return TrailResult{Trail: tr, Problem: err.Error()}
	}
	return TrailResult{Trail: tr}
}

// chatChanged is called by the agent manager, from its own goroutines, every
// time a session changes. It tells the page, at a bounded rate, and raises a
// notification when a session starts waiting for permission.
func (a *App) chatChanged(id string) {
	a.mu.Lock()
	ctx := a.ctx
	if ctx == nil || a.closing || a.chatTimers[id] {
		a.mu.Unlock()
		return
	}
	a.chatTimers[id] = true
	a.mu.Unlock()

	time.AfterFunc(chatEventGap, func() {
		a.mu.Lock()
		delete(a.chatTimers, id)
		closing := a.closing
		a.mu.Unlock()
		if closing {
			return
		}
		runtime.EventsEmit(ctx, chatEvent, id)
		a.announceWaiting(ctx, id)
	})
}

// announceWaiting notifies once when a session begins to wait for the user's
// permission: the agent is stopped until someone answers. The notification
// names the project and the tool, never what the tool was asked to do.
func (a *App) announceWaiting(ctx context.Context, id string) {
	v, err := a.agents.Get(id, 1<<62)
	if err != nil {
		return
	}
	a.mu.Lock()
	was := a.chatStates[id]
	a.chatStates[id] = v.State
	announce := a.notify && a.notifyReady && v.State == agent.StateWaiting && was != agent.StateWaiting
	privacy := a.privacy
	a.mu.Unlock()
	if !announce {
		return
	}
	title, body := "Một session cần bạn duyệt", "Agent đang chờ bạn cho phép."
	if !privacy {
		title = clean.Text(filepath.Base(v.Dir), 60) + ": cần bạn duyệt"
		if len(v.Pending) > 0 {
			body = "Agent xin phép dùng " + v.Pending[0].Tool + "."
		}
	}
	a.send(ctx, title, body)
}
