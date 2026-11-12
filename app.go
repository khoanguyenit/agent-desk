package main

import (
	"context"
	"fmt"
	"math"
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
	"monitor-agent/internal/history"
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
	// snapshotEvent tells the page a new scan is ready to be fetched, so it
	// does not have to ask on a timer of its own. historyEvent says the same
	// of the history, and focusEvent carries where a clicked notification
	// points.
	snapshotEvent = "snapshot"
	historyEvent  = "history"
	focusEvent    = "focus"
	// noticeEvent carries each thing the user is told, for the list the page
	// keeps: Windows may be set not to show notifications at all.
	noticeEvent = "notice"

	// The history is saved this often while it changes, and old transcripts
	// are looked through again this often for sessions that ran while the
	// app was closed or was not looking.
	historySaveEvery = 2 * time.Minute
	historyFillEvery = 6 * time.Hour
	// maxBudgetUSD bounds the daily limit the page may set.
	maxBudgetUSD = 1_000_000
	// targetsKept is how many notifications are remembered for a click.
	targetsKept = 64
	// noticesKept is how many notices the in-app list goes back.
	noticesKept = 40
	// toastCheckEvery is how often Windows is asked again whether it shows
	// this app's notifications.
	toastCheckEvery = 10 * time.Second
)

// Notice is one thing the app told the user. It is a line of the list in the
// window, and the text of the Windows notification when Windows shows those.
type Notice struct {
	ID    int    `json:"id"`
	AtMs  int64  `json:"atMs"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// Where it leads when clicked; see focusTarget.
	ProjectPath string `json:"projectPath"`
	SessionID   string `json:"sessionId"`
	Chat        bool   `json:"chat"`
}

// focusTarget is where a notification leads when it is clicked.
type focusTarget struct {
	ProjectPath string `json:"projectPath"`
	SessionID   string `json:"sessionId"`
	// Chat is true when the session is the app's own and its conversation
	// should be opened.
	Chat bool `json:"chat"`
}

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
	pageReady atomic.Bool // the page has loaded and can be sent events
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
	histProblem string // why the saved history could not be read at start-up
	saveProblem string // why the history could not be saved just now
	budget      float64
	targets     map[string]focusTarget // where each recent notification leads
	notices     []Notice               // the latest things said, oldest first
	toastWhy    string                 // why Windows will not show notifications, if it will not
	toastAt     time.Time              // when that was last looked up
	sent        int
	chatTimers  map[string]bool   // sessions with a chat event already scheduled
	chatStates  map[string]string // last state seen per session, to spot a new wait
}

// NewApp creates the application with the real environment.
func NewApp() *App {
	opts, notice, locked := monitorOptions()
	// A locked development build keeps nothing on disk: its history lives
	// and dies with the process, like its project list.
	var listPath, historyPath string
	if !locked {
		if dir, err := os.UserConfigDir(); err == nil {
			listPath = filepath.Join(dir, "monitor-agent", "projects.json")
			historyPath = filepath.Join(dir, "monitor-agent", "history.json")
		}
	}
	ledger, historyErr := history.Open(historyPath)
	opts.History = ledger

	a := &App{
		svc:        monitor.New(opts),
		notice:     notice,
		locked:     locked,
		ready:      make(chan struct{}),
		chatTimers: map[string]bool{},
		chatStates: map[string]string{},
		targets:    map[string]focusTarget{},
	}
	if historyErr != nil {
		a.histProblem = "Không đọc được lịch sử chi phí đã lưu (" + clean.Text(historyErr.Error(), 200) +
			"). Lịch sử được dựng lại từ transcript còn trên đĩa; file cũ được giữ lại dưới tên history.json.bak."
	}
	a.agents = agent.NewManager(a.chatChanged)

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
	// What the day cost so far is kept for the next start.
	_ = a.svc.SaveHistory()
}

// domReady is called by Wails once the page has loaded.
func (a *App) domReady(context.Context) { a.pageReady.Store(true) }

// tell sends the page an event, once there is a page to hear it.
func (a *App) tell(event string, data ...interface{}) {
	a.mu.Lock()
	ctx, closing := a.ctx, a.closing
	a.mu.Unlock()
	if ctx == nil || closing || !a.pageReady.Load() {
		return
	}
	runtime.EventsEmit(ctx, event, data...)
}

func (a *App) watch(ctx context.Context) {
	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	a.refresh()
	// The board comes first. Only then are older transcripts read, and the
	// sessions the board is showing are already in the history by then.
	go a.keepHistory(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.refresh()
		}
	}
}

// keepHistory reads the transcripts of earlier days into the history, now and
// every few hours, and saves the history while it changes.
func (a *App) keepHistory(ctx context.Context) {
	// note records how the latest save went. One that worked clears an
	// earlier complaint.
	note := func(what string, err error) {
		problem := ""
		if err != nil {
			problem = what + ": " + clean.Text(err.Error(), 200)
		}
		a.mu.Lock()
		a.saveProblem = problem
		a.mu.Unlock()
	}
	fill := func() {
		defer func() {
			if r := recover(); r != nil {
				note("Lỗi nội bộ khi đọc lịch sử", fmt.Errorf("%v", r))
			}
		}()
		err := a.svc.Backfill(ctx)
		if ctx.Err() == nil {
			note("Không lưu được lịch sử chi phí", err)
			a.tell(historyEvent)
		}
	}
	fill()
	save := time.NewTicker(historySaveEvery)
	defer save.Stop()
	again := time.NewTicker(historyFillEvery)
	defer again.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-save.C:
			note("Không lưu được lịch sử chi phí", a.svc.SaveHistory())
		case <-again.C:
			fill()
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
	a.checkToasts()

	a.mu.Lock()
	prev, had := a.latest, a.have
	a.latest, a.have = snap, true
	// Whether Windows will show them or not: the window lists them too.
	announce := had && a.notify
	privacy, ctx, budget := a.privacy, a.ctx, a.budget
	a.mu.Unlock()
	a.readyOnce.Do(func() { close(a.ready) })
	a.tell(snapshotEvent)

	if !announce {
		return
	}
	for _, e := range monitor.Diff(prev, snap) {
		title, body := e.Text(privacy)
		a.send(ctx, title, body, focusTarget{ProjectPath: e.ProjectPath, SessionID: e.SessionID})
	}
	if monitor.BudgetCrossed(prev, snap, budget) {
		a.send(ctx, "Chi phí hôm nay đã vượt ngưỡng bạn đặt",
			fmt.Sprintf("Đã tiêu $%.2f, ngưỡng là $%.2f. Đây là số quy theo giá API.", snap.TodaySpend.CostUSD, budget), focusTarget{})
	}
}

// send tells the user something, three ways at once, because none of them is
// sure to arrive alone: a line in the window's own list, a flashing taskbar
// button when the window is not in front, and a Windows notification, which
// Windows shows only if it has not been told otherwise. target is where a
// click leads; the zero value leads nowhere in particular and only brings the
// app forward.
func (a *App) send(ctx context.Context, title, body string, target focusTarget) {
	a.mu.Lock()
	a.sent++
	id := "monitor-agent-" + strconv.Itoa(a.sent)
	if target != (focusTarget{}) {
		a.targets[id] = target
	}
	delete(a.targets, "monitor-agent-"+strconv.Itoa(a.sent-targetsKept))
	note := Notice{
		ID: a.sent, AtMs: time.Now().UnixMilli(), Title: title, Body: body,
		ProjectPath: target.ProjectPath, SessionID: target.SessionID, Chat: target.Chat,
	}
	a.notices = append(a.notices, note)
	if n := len(a.notices); n > noticesKept {
		a.notices = append([]Notice(nil), a.notices[n-noticesKept:]...)
	}
	toast := a.notifyReady
	a.mu.Unlock()

	a.tell(noticeEvent, note)
	flashWindow()
	if !toast {
		return
	}

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
	for _, p := range []string{a.histProblem, a.saveProblem} {
		if p != "" {
			snap.Warnings = append(snap.Warnings, p)
		}
	}
	if a.problem != "" {
		snap.Warnings = append(snap.Warnings, a.problem)
	}
	if a.notify && a.toastWhy != "" {
		snap.Warnings = append(snap.Warnings, a.toastWhy+" Trong lúc đó app vẫn báo trong cửa sổ này (nút Thông báo) và nhấp nháy trên taskbar.")
	}
	return snap
}

// checkToasts looks up, now and then, whether Windows is set not to show this
// app's notifications. Windows drops them without a word in that case, so
// the app would otherwise never know why nothing appears.
func (a *App) checkToasts() {
	a.mu.Lock()
	due := a.notify && time.Since(a.toastAt) >= toastCheckEvery
	if due {
		a.toastAt = time.Now()
	}
	a.mu.Unlock()
	if !due {
		return
	}
	why := toastBlock()
	a.mu.Lock()
	a.toastWhy = why
	a.mu.Unlock()
}

// GetNotices returns the latest things the app told the user, oldest first.
func (a *App) GetNotices() []Notice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Notice{}, a.notices...)
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
			return "Không đăng ký được thông báo với Windows (" + clean.Text(err.Error(), 200) + "). App vẫn báo trong cửa sổ này và nhấp nháy trên taskbar."
		}
		// Clicking a notification brings the app forward, and takes the
		// page to what the notification was about when that is known.
		runtime.OnNotificationResponse(ctx, func(res runtime.NotificationResult) {
			runtime.WindowUnminimise(ctx)
			runtime.WindowShow(ctx)
			a.mu.Lock()
			target, known := a.targets[res.Response.ID]
			a.mu.Unlock()
			if known {
				a.tell(focusEvent, target)
			}
		})
		a.mu.Lock()
		a.notifyReady = true
		a.mu.Unlock()
	}
	if announce {
		a.send(ctx, "Agent Desk", "Đã bật thông báo. Bạn sẽ được báo khi một session xong lượt, cần bạn trả lời, hoặc một agent làm xong.", focusTarget{})
	}
	return ""
}

// SetBudget records the cost per day, in dollars at API list price, past
// which the user wants to be told. Zero means no limit. The notification is
// sent once, when the day's cost crosses it, and only while notifications are
// on. It returns an empty string, or what is wrong with the figure.
func (a *App) SetBudget(usd float64) string {
	if math.IsNaN(usd) || usd < 0 || usd > maxBudgetUSD {
		return "Ngưỡng chi phí phải là một số từ 0 tới " + strconv.Itoa(maxBudgetUSD) + " USD."
	}
	a.mu.Lock()
	a.budget = usd
	a.mu.Unlock()
	return ""
}

// GetHistory returns what was consumed on each of the last days, today
// included. It answers from what the app has kept; reading old transcripts
// goes on in the background, and History.Scan says how far it has come.
func (a *App) GetHistory(days int) monitor.History {
	return a.svc.History(days)
}

// ExportHistory writes the last days of history to a CSV file the user picks
// in a dialog of the operating system: the page cannot choose where it goes.
// It returns an empty string, or what went wrong; cancelling is not an error.
func (a *App) ExportHistory(days int) string {
	ctx := a.context()
	if ctx == nil {
		return "Ứng dụng chưa sẵn sàng."
	}
	if !a.asking.CompareAndSwap(false, true) {
		return ""
	}
	defer a.asking.Store(false)
	rows := a.svc.HistoryRows(days)
	if len(rows) == 0 {
		return "Chưa có số liệu nào trong khoảng này để xuất."
	}
	name := "agent-desk-chi-phi-" + rows[0].Day + "_" + rows[len(rows)-1].Day + ".csv"
	path, err := runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
		Title:           "Xuất lịch sử chi phí",
		DefaultFilename: name,
		Filters:         []runtime.FileFilter{{DisplayName: "CSV (*.csv)", Pattern: "*.csv"}},
	})
	if err != nil {
		return "Không mở được hộp lưu file: " + err.Error()
	}
	if path == "" {
		return ""
	}
	if err := writeFile(path, func(f *os.File) error { return monitor.WriteHistoryCSV(f, rows) }); err != nil {
		return "Không ghi được file: " + clean.Text(err.Error(), 200)
	}
	return ""
}

// writeFile creates path, lets fill write to it, and reports the first thing
// that went wrong, a failed close included: on a full disk that is where the
// loss shows.
func writeFile(path string, fill func(*os.File) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := fill(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// GetModels lists the models a new session can be started with.
func (a *App) GetModels() []monitor.ModelChoice {
	return a.svc.ModelChoices()
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

// offered reports whether model is one the app offers for a new session; the
// empty name, which leaves the choice to Claude Code, always is.
func (a *App) offered(model string) bool {
	if model == "" {
		return true
	}
	for _, c := range a.svc.ModelChoices() {
		if c.ID == model {
			return true
		}
	}
	return false
}

const modelProblem = "Model này không nằm trong danh sách app đưa ra."

// StartSession opens a new Claude Code session in a project and sends it the
// user's first request. mode is "ask", "acceptEdits" or "plan"; model is one
// of GetModels, or empty for Claude Code's own default. The project must be
// one the user has agreed to (see TrustProject): that is checked here, not
// left to the page.
func (a *App) StartSession(projectPath, prompt, mode, model string) StartResult {
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
	if !a.offered(model) {
		return StartResult{Problem: modelProblem}
	}
	id, err := a.agents.Open(agent.Options{Dir: p.Dir(), Prompt: prompt, Mode: mode, Model: model})
	if err != nil {
		return StartResult{ID: id, Problem: err.Error()}
	}
	return StartResult{ID: id}
}

// ResumeSession opens again a session that has ended and sends it the user's
// next request. Only a session the board lists as closed today can be named,
// it is opened in the folder it ran in, and that folder must be one the user
// has agreed to, exactly as for a new session.
func (a *App) ResumeSession(sessionID, prompt, mode, model string) StartResult {
	if a.locked {
		return StartResult{Problem: lockedMessage}
	}
	dir, err := a.svc.Resumable(sessionID)
	if err != nil {
		return StartResult{Problem: err.Error()}
	}
	if !a.projects.Has(dir) {
		return StartResult{Problem: "Thư mục của session này chưa được bạn xác nhận tin tưởng. Bấm \"Tiếp tục session\" lại để xác nhận."}
	}
	if !a.offered(model) {
		return StartResult{Problem: modelProblem}
	}
	id, err := a.agents.Open(agent.Options{Dir: dir, Prompt: prompt, Mode: mode, Model: model, Resume: sessionID})
	if err != nil {
		return StartResult{ID: id, Problem: err.Error()}
	}
	// The session is running again; the board should stop listing it as
	// closed without waiting for the next tick.
	go a.refresh()
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

// GetTrail returns the latest steps of a session's main thread, or of one of
// its agents when agentID is given. It is read-only, and reaches only
// sessions that are on the board: the running ones and the ones listed as
// closed today. With "Ẩn nội dung" on, what was said and run is left out.
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
	announce := a.notify && v.State == agent.StateWaiting && was != agent.StateWaiting
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
	a.send(ctx, title, body, focusTarget{SessionID: v.ID, Chat: true})
}
