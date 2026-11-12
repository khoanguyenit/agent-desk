// Package monitor assembles the view the UI shows: every running Claude Code
// session grouped by project, its subagents, what each is doing, and what all
// of it has consumed and cost.
package monitor

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"monitor-agent/internal/clean"
	"monitor-agent/internal/parse"
	"monitor-agent/internal/pricing"
	"monitor-agent/internal/scan"
	"monitor-agent/internal/usage"
)

const (
	// An unfinished agent that wrote to its transcript this recently is
	// counted as running.
	runningWithin = 2 * time.Minute
	// Beyond runningWithin and up to this, it is "quiet": possibly inside a
	// long model turn, possibly stuck. Past it, the agent was most likely
	// interrupted without being marked finished.
	quietWithin = 15 * time.Minute
	// An agent whose last act was a tool call with no result yet is waiting
	// on that tool. Builds and test suites run long, so it stays "quiet" for
	// much longer before it is written off.
	awaitingWithin = 3 * time.Hour
	// A finished flag is believed unless the transcript shows work this long
	// after the flag was written.
	finishedSlack = 5 * time.Second
	// Parser state of a file is kept this many refreshes after it was last
	// needed, so a session that blinks out of the registry for a moment does
	// not have its transcripts read again from the start.
	forgetAfter = 30
	// PulseMinutes is the length of the per-minute activity strip.
	PulseMinutes = 30

	labelRunes = 80
	pathRunes  = 300
)

// Agent states.
const (
	StatusRunning = "running"
	StatusQuiet   = "quiet"
	StatusStopped = "stopped"
	StatusDone    = "done"
)

// CostParts is a cost in dollars split by what was paid for.
type CostParts struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	WebSearch  float64 `json:"webSearch"`
}

// Spend is token consumption with its cost.
type Spend struct {
	Tokens      usage.Tokens `json:"tokens"`
	TotalTokens int64        `json:"totalTokens"`
	CostUSD     float64      `json:"costUsd"`
	CostParts   CostParts    `json:"costParts"`
	// UnpricedTokens were consumed by models missing from the price table and
	// are not part of CostUSD.
	UnpricedTokens int64 `json:"unpricedTokens"`
}

// Activity is the latest thing an agent did.
type Activity struct {
	Kind   string `json:"kind"`
	Tool   string `json:"tool"`
	Detail string `json:"detail"`
	AtMs   int64  `json:"atMs"`
}

// Agent is one subagent of a session.
type Agent struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Model       string `json:"model"`
	ModelLabel  string `json:"modelLabel"`
	Status      string `json:"status"`
	// AwaitingTool is true when the agent's last act was a tool call that has
	// not returned: it is waiting on that tool rather than silent.
	AwaitingTool  bool     `json:"awaitingTool"`
	WorkflowID    string   `json:"workflowId"`
	Phase         string   `json:"phase"`
	Background    bool     `json:"background"`
	StartedAtMs   int64    `json:"startedAtMs"`
	LastAtMs      int64    `json:"lastAtMs"`
	DurationMs    int64    `json:"durationMs"`
	SilentMs      int64    `json:"silentMs"`
	ContextTokens int64    `json:"contextTokens"`
	ToolUses      int      `json:"toolUses"`
	All           Spend    `json:"all"`
	Today         Spend    `json:"today"`
	Activity      Activity `json:"activity"`
	Pulse         []int    `json:"pulse"`
}

// Session is one running Claude Code process.
type Session struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Title         string   `json:"title"`
	PID           int      `json:"pid"`
	Status        string   `json:"status"`
	Entrypoint    string   `json:"entrypoint"`
	Version       string   `json:"version"`
	GitBranch     string   `json:"gitBranch"`
	StartedAtMs   int64    `json:"startedAtMs"`
	LastAtMs      int64    `json:"lastAtMs"`
	Model         string   `json:"model"`
	ModelLabel    string   `json:"modelLabel"`
	ContextTokens int64    `json:"contextTokens"`
	LastPrompt    string   `json:"lastPrompt"`
	Activity      Activity `json:"activity"`
	Pulse         []int    `json:"pulse"`
	// Own is the main thread alone; All and Today include the subagents.
	Own    Spend   `json:"own"`
	All    Spend   `json:"all"`
	Today  Spend   `json:"today"`
	Agents []Agent `json:"agents"`
	// ActiveAgents counts agents that are running or quiet.
	ActiveAgents int `json:"activeAgents"`
	// RecordedCostUSD is the cost Claude Code last wrote for this session, or
	// a negative number when the transcript has none.
	RecordedCostUSD float64 `json:"recordedCostUsd"`
	HasTranscript   bool    `json:"hasTranscript"`
	// Duplicate marks a session whose transcript is already shown under
	// another running process. Its figures are listed but added to the
	// totals only once.
	Duplicate bool `json:"duplicate"`
	// Managed marks a session this app started itself, and can therefore be
	// talked to from the app. It is set by the app, not by the scan.
	Managed bool `json:"managed"`
	// AwaitingUser marks a managed session that has stopped to ask the user
	// for permission. Also set by the app.
	AwaitingUser bool `json:"awaitingUser"`
}

// Project groups the sessions that share a working directory.
type Project struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Sessions []Session `json:"sessions"`
	All      Spend     `json:"all"`
	Today    Spend     `json:"today"`
	// Pinned marks a project the user added by hand. It is listed even when
	// no session is running in it.
	Pinned bool `json:"pinned"`

	// dir is the working directory exactly as the session registered it.
	// Path is the same value tidied for display.
	dir string
}

// PinnedProject is the entry for a folder the user added, for showing it
// while nothing is running there.
func PinnedProject(dir string) Project {
	return Project{
		Name:     clean.Text(filepath.Base(filepath.Clean(dir)), labelRunes),
		Path:     clean.Text(dir, pathRunes),
		Sessions: []Session{},
		Pinned:   true,
		dir:      dir,
	}
}

// Dir is the project's folder as registered, for starting a program in it.
func (p Project) Dir() string { return p.dir }

// EditorURI is the vscode:// address of the project's folder. VS Code focuses
// the window that already has the folder open, and opens one if none has.
func (p Project) EditorURI() string {
	segments := strings.Split(filepath.ToSlash(p.dir), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	path := strings.Join(segments, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "vscode://file" + path
}

// ModelRow is consumption by one model across everything shown.
type ModelRow struct {
	Model    string `json:"model"`
	Label    string `json:"label"`
	Priced   bool   `json:"priced"`
	Verified bool   `json:"verified"`
	All      Spend  `json:"all"`
	Today    Spend  `json:"today"`
}

// Counts summarises how much is going on.
type Counts struct {
	Projects      int `json:"projects"`
	Sessions      int `json:"sessions"`
	BusySessions  int `json:"busySessions"`
	Agents        int `json:"agents"`
	RunningAgents int `json:"runningAgents"`
	QuietAgents   int `json:"quietAgents"`
}

// Snapshot is everything the UI renders.
type Snapshot struct {
	GeneratedAtMs int64      `json:"generatedAtMs"`
	Today         string     `json:"today"`
	ConfigDir     string     `json:"configDir"`
	Counts        Counts     `json:"counts"`
	All           Spend      `json:"all"`
	TodaySpend    Spend      `json:"todaySpend"`
	Projects      []Project  `json:"projects"`
	Models        []ModelRow `json:"models"`
	PricingAsOf   string     `json:"pricingAsOf"`
	PricingSource string     `json:"pricingSource"`
	PricingPath   string     `json:"pricingPath"`
	Warnings      []string   `json:"warnings"`
}

// Options configures a Service. Zero values select the real environment.
type Options struct {
	ConfigDir   string
	PricingPath string
	Alive       scan.AliveFunc
	Now         func() time.Time
	Location    *time.Location
}

type fileEntry struct {
	file   *parse.File
	unused int
}

type metaEntry struct {
	meta scan.AgentMeta
	mod  time.Time
	size int64
}

// Service builds snapshots and keeps per-file parser state between them, so
// each refresh only reads what was appended.
type Service struct {
	mu          sync.Mutex
	configDir   string
	configErr   error
	pricingPath string
	alive       scan.AliveFunc
	now         func() time.Time
	loc         *time.Location
	registry    scan.Registry
	files       map[string]*fileEntry
	metas       map[string]metaEntry
}

// New creates a Service.
func New(o Options) *Service {
	s := &Service{
		configDir:   o.ConfigDir,
		pricingPath: o.PricingPath,
		alive:       o.Alive,
		now:         o.Now,
		loc:         o.Location,
		files:       map[string]*fileEntry{},
		metas:       map[string]metaEntry{},
	}
	if s.configDir == "" {
		s.configDir, s.configErr = scan.ConfigDir()
	}
	if s.pricingPath == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			s.pricingPath = filepath.Join(dir, "monitor-agent", "pricing.json")
		}
	}
	if s.alive == nil {
		s.alive = scan.ProcessAlive
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.loc == nil {
		s.loc = time.Local
	}
	return s
}

// acc accumulates spend in exact integer units.
type acc struct {
	tokens   usage.Tokens
	cost     pricing.Breakdown
	unpriced int64
}

func (a *acc) add(o acc) {
	a.tokens.Add(o.tokens)
	a.cost.Add(o.cost)
	a.unpriced += o.unpriced
}

func dollars(nano int64) float64 { return float64(nano) / pricing.NanoPerUSD }

func (a acc) spend() Spend {
	return Spend{
		Tokens:      a.tokens,
		TotalTokens: a.tokens.Total(),
		CostUSD:     dollars(a.cost.Total()),
		CostParts: CostParts{
			Input:      dollars(a.cost.Input),
			Output:     dollars(a.cost.Output),
			CacheRead:  dollars(a.cost.CacheRead),
			CacheWrite: dollars(a.cost.CacheWrite),
			WebSearch:  dollars(a.cost.WebSearch),
		},
		UnpricedTokens: a.unpriced,
	}
}

type modelAcc struct {
	all, today acc
	priced     bool
	verified   bool
}

// build holds the state of one Snapshot call.
type build struct {
	s      *Service
	now    time.Time
	today  string
	prices pricing.Table
	models map[string]*modelAcc
	// tallied holds the transcripts already added to the totals, so a
	// session open in two processes is counted once.
	tallied map[string]bool
	// Problems met along the way, reported once as warnings.
	staleFiles int
	badLines   int
	badUsage   int
}

// Snapshot scans the machine and returns the current picture. It does not
// fail: problems are reported in Snapshot.Warnings.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	snap := Snapshot{
		GeneratedAtMs: now.UnixMilli(),
		Today:         now.In(s.loc).Format("2006-01-02"),
		ConfigDir:     s.configDir,
		PricingPath:   s.pricingPath,
		Projects:      []Project{},
		Models:        []ModelRow{},
		Warnings:      []string{},
	}

	prices, err := pricing.Load(s.pricingPath)
	if err != nil {
		snap.Warnings = append(snap.Warnings, "Bảng giá tuỳ chỉnh bị lỗi, đang dùng bảng giá mặc định: "+clean.Text(err.Error(), pathRunes))
	}
	snap.PricingAsOf, snap.PricingSource = prices.AsOf, prices.Source

	if s.configErr != nil {
		snap.Warnings = append(snap.Warnings, "Không xác định được thư mục cấu hình Claude Code: "+s.configErr.Error())
		return snap
	}
	sessions, err := s.registry.Live(s.configDir, s.alive)
	if err != nil {
		snap.Warnings = append(snap.Warnings, "Không đọc được danh sách session: "+clean.Text(err.Error(), pathRunes))
		return snap
	}

	b := &build{
		s:       s,
		now:     now,
		today:   snap.Today,
		prices:  prices,
		models:  map[string]*modelAcc{},
		tallied: map[string]bool{},
	}
	for _, e := range s.files {
		e.unused++
	}

	type projectAcc struct {
		project    Project
		all, today acc
	}
	byProject := map[string]*projectAcc{}
	var order []string
	var total, totalToday acc
	for _, rs := range sessions {
		sess, all, today := b.session(rs)
		key := projectKey(rs.Cwd)
		p, ok := byProject[key]
		if !ok {
			p = &projectAcc{project: Project{
				Name: clean.Text(filepath.Base(filepath.Clean(rs.Cwd)), labelRunes),
				Path: clean.Text(rs.Cwd, pathRunes),
				dir:  rs.Cwd,
			}}
			byProject[key] = p
			order = append(order, key)
		}
		p.project.Sessions = append(p.project.Sessions, sess)
		if !sess.Duplicate {
			p.all.add(all)
			p.today.add(today)
			total.add(all)
			totalToday.add(today)
		}

		snap.Counts.Sessions++
		if sess.Status == "busy" {
			snap.Counts.BusySessions++
		}
		if sess.Duplicate {
			continue
		}
		for _, a := range sess.Agents {
			snap.Counts.Agents++
			switch a.Status {
			case StatusRunning:
				snap.Counts.RunningAgents++
			case StatusQuiet:
				snap.Counts.QuietAgents++
			}
		}
	}

	// Forget files that have not been needed for a while.
	for path, e := range s.files {
		if e.unused > forgetAfter {
			delete(s.files, path)
		}
	}
	for path := range s.metas {
		if _, ok := s.files[transcriptOf(path)]; !ok {
			delete(s.metas, path)
		}
	}

	slices.Sort(order)
	for _, key := range order {
		p := byProject[key]
		p.project.All, p.project.Today = p.all.spend(), p.today.spend()
		snap.Projects = append(snap.Projects, p.project)
	}
	snap.Counts.Projects = len(snap.Projects)
	snap.All, snap.TodaySpend = total.spend(), totalToday.spend()

	var unpriced, unverified []string
	for name, m := range b.models {
		snap.Models = append(snap.Models, ModelRow{
			Model:    name,
			Label:    ModelLabel(name),
			Priced:   m.priced,
			Verified: m.verified,
			All:      m.all.spend(),
			Today:    m.today.spend(),
		})
		switch {
		case !m.priced:
			unpriced = append(unpriced, ModelLabel(name))
		case !m.verified:
			unverified = append(unverified, ModelLabel(name))
		}
	}
	sort.Slice(snap.Models, func(i, j int) bool {
		a, c := snap.Models[i], snap.Models[j]
		if a.All.TotalTokens != c.All.TotalTokens {
			return a.All.TotalTokens > c.All.TotalTokens
		}
		return a.Model < c.Model
	})
	if len(unpriced) > 0 {
		slices.Sort(unpriced)
		snap.Warnings = append(snap.Warnings,
			"Chưa có giá cho model: "+strings.Join(unpriced, ", ")+". Token vẫn được đếm nhưng chưa tính vào chi phí.")
	}
	if len(unverified) > 0 {
		slices.Sort(unverified)
		snap.Warnings = append(snap.Warnings,
			"Giá cache của "+strings.Join(unverified, ", ")+" chưa đối chiếu được với số liệu thực tế, chi phí của các model này là ước tính.")
	}
	if b.staleFiles > 0 {
		snap.Warnings = append(snap.Warnings,
			fmt.Sprintf("Không đọc được %d transcript ở lần cập nhật này, đang hiện số liệu đã đọc trước đó.", b.staleFiles))
	}
	if b.badUsage > 0 {
		snap.Warnings = append(snap.Warnings,
			fmt.Sprintf("Có %d lượt gọi API ghi số token theo dạng app không hiểu nên chưa được cộng. Có thể Claude Code đã đổi định dạng transcript.", b.badUsage))
	}
	if b.badLines > 0 {
		snap.Warnings = append(snap.Warnings,
			fmt.Sprintf("Bỏ qua %d dòng transcript không đọc được.", b.badLines))
	}
	return snap
}

func projectKey(cwd string) string {
	k := filepath.Clean(cwd)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		k = strings.ToLower(k)
	}
	return k
}

// transcriptOf maps an agent's meta file path back to its transcript path.
func transcriptOf(metaPath string) string {
	return strings.TrimSuffix(metaPath, ".meta.json") + ".jsonl"
}

// file returns the parser state for path, creating it on first use, and
// brings it up to date. The state is returned even when the update fails: it
// then holds what was read before, which is still worth showing. ok is false
// only when nothing has ever been read from the file.
func (b *build) file(path string, sidechain bool) (f *parse.File, ok bool) {
	e, found := b.s.files[path]
	if !found {
		e = &fileEntry{file: parse.NewFile(path, b.s.loc, sidechain)}
		b.s.files[path] = e
	}
	e.unused = 0
	if err := e.file.Update(); err != nil {
		if e.file.Offset() == 0 {
			return e.file, false
		}
		b.staleFiles++
	}
	b.badLines += e.file.BadLines
	b.badUsage += e.file.BadUsage
	return e.file, true
}

// price costs a set of usage buckets. When count is true the result also
// goes into the per-model totals.
func (b *build) price(buckets map[parse.Key]usage.Tokens, count bool) (all, today acc) {
	for k, tk := range buckets {
		cost, ok := b.prices.Price(k.Model, k.Fast, tk)
		one := acc{tokens: tk, cost: cost}
		if !ok {
			one.unpriced = tk.Total()
		}
		all.add(one)
		isToday := k.Day == b.today
		if isToday {
			today.add(one)
		}
		if !count {
			continue
		}
		name := pricing.Normalize(k.Model)
		m := b.models[name]
		if m == nil {
			m = &modelAcc{}
			if r, found := b.prices.Lookup(name); found {
				m.priced, m.verified = true, r.Verified
			}
			b.models[name] = m
		}
		m.all.add(one)
		if isToday {
			m.today.add(one)
		}
	}
	return all, today
}

// ownResponses returns the usage of an agent's transcript without the
// responses that already belong to the session's main transcript or to an
// agent listed earlier. A forked agent's transcript begins with a copy of the
// parent response that spawned it; counting that copy would bill it twice.
func ownResponses(f, main *parse.File, claimed map[string]bool) map[parse.Key]usage.Tokens {
	out := map[parse.Key]usage.Tokens{}
	f.Responses(func(id string, k parse.Key, tk usage.Tokens) {
		// Only ids issued by the API identify the same response across
		// files; the fallback ids made up for lines without one do not.
		if strings.HasPrefix(id, "msg_") {
			if main.HasResponse(id) || claimed[id] {
				return
			}
			claimed[id] = true
		}
		cur := out[k]
		cur.Add(tk)
		out[k] = cur
	})
	return out
}

func (b *build) session(rs scan.Session) (Session, acc, acc) {
	sess := Session{
		ID:              rs.SessionID,
		Name:            clean.Text(rs.Name, labelRunes),
		PID:             rs.PID,
		Status:          clean.Text(rs.Status, labelRunes),
		Entrypoint:      clean.Text(rs.Entrypoint, labelRunes),
		Version:         clean.Text(rs.Version, labelRunes),
		StartedAtMs:     ms(rs.StartedAt),
		Agents:          []Agent{},
		Pulse:           make([]int, PulseMinutes),
		RecordedCostUSD: -1,
	}
	var all, today acc

	path, ok := scan.TranscriptPath(b.s.configDir, rs)
	if !ok {
		// A session that has not exchanged a message yet has no transcript.
		return sess, all, today
	}
	f, ok := b.file(path, false)
	if !ok {
		return sess, all, today
	}
	count := !b.tallied[path]
	b.tallied[path] = true
	sess.Duplicate = !count

	sess.HasTranscript = true
	sess.Title = f.Title
	sess.GitBranch = f.GitBranch
	sess.LastAtMs = ms(f.LastAt)
	sess.Model = f.Model
	sess.ModelLabel = ModelLabel(f.Model)
	sess.ContextTokens = f.ContextTokens
	sess.LastPrompt = f.LastPrompt
	sess.Activity = activity(f.Last, rs.Cwd)
	sess.Pulse = f.Pulse(b.now, PulseMinutes)
	if f.HasRecordedCost {
		sess.RecordedCostUSD = f.RecordedCostUSD
	}

	own, ownToday := b.price(f.Usage, count)
	sess.Own = own.spend()
	all.add(own)
	today.add(ownToday)

	claimed := map[string]bool{}
	for _, af := range scan.AgentFiles(path) {
		a, aAll, aToday, ok := b.agent(af, rs.Cwd, f, claimed, count)
		if !ok {
			continue
		}
		sess.Agents = append(sess.Agents, a)
		if a.Status == StatusRunning || a.Status == StatusQuiet {
			sess.ActiveAgents++
		}
		all.add(aAll)
		today.add(aToday)
	}
	sortAgents(sess.Agents)

	sess.All, sess.Today = all.spend(), today.spend()
	return sess, all, today
}

// meta returns an agent's meta file, read again only when the file changed.
// modTime is when the meta was last written, or zero when that is unknown.
func (b *build) meta(path string) (scan.AgentMeta, time.Time) {
	cached, have := b.s.metas[path]
	st, err := os.Stat(path)
	if err != nil {
		// Missing or unreadable right now: keep what was known.
		return cached.meta, cached.mod
	}
	if have && st.ModTime().Equal(cached.mod) && st.Size() == cached.size {
		return cached.meta, cached.mod
	}
	m, err := scan.ReadMeta(path)
	if err != nil {
		return cached.meta, cached.mod
	}
	b.s.metas[path] = metaEntry{meta: m, mod: st.ModTime(), size: st.Size()}
	return m, st.ModTime()
}

func (b *build) agent(af scan.AgentFile, cwd string, main *parse.File, claimed map[string]bool, count bool) (Agent, acc, acc, bool) {
	f, ok := b.file(af.Path, true)
	if !ok {
		return Agent{}, acc{}, acc{}, false
	}
	meta, metaMod := b.meta(af.MetaPath)

	model := f.Model
	if model == "" {
		model = clean.Text(meta.Model, labelRunes)
	}
	if model == "" {
		model = clean.Text(meta.PickedModel, labelRunes)
	}
	a := Agent{
		ID:            af.ID,
		Type:          clean.Text(meta.AgentType, labelRunes),
		Description:   clean.Snippet(meta.Description, 120),
		Model:         model,
		ModelLabel:    ModelLabel(model),
		WorkflowID:    af.WorkflowID,
		Phase:         clean.Text(meta.WorkflowPhase, labelRunes),
		Background:    meta.RequestShape == "background",
		StartedAtMs:   ms(f.FirstAt),
		LastAtMs:      ms(f.LastAt),
		ContextTokens: f.ContextTokens,
		ToolUses:      f.ToolUses,
		Activity:      activity(f.Last, cwd),
		Pulse:         f.Pulse(b.now, PulseMinutes),
	}
	if a.Description == "" {
		a.Description = a.Type
	}

	// When the transcript carries no timestamps yet, the file's own
	// modification time stands in for the last sign of life.
	lastSeen := f.LastAt
	if lastSeen.IsZero() {
		lastSeen = f.ModTime
	}
	var silent time.Duration
	if !lastSeen.IsZero() {
		silent = max(b.now.Sub(lastSeen), 0)
	}

	// The transcript is the better witness. The finished flag is believed
	// too, unless the transcript shows work after the flag was written: an
	// agent that was asked to continue is working again.
	ended := f.TurnEnded
	if !ended && meta.Finished {
		ended = metaMod.IsZero() || !f.LastAt.After(metaMod.Add(finishedSlack))
	}
	switch {
	case ended:
		a.Status = StatusDone
	case silent <= runningWithin:
		a.Status = StatusRunning
	case f.AwaitingTool && silent <= awaitingWithin:
		a.Status = StatusQuiet
		a.AwaitingTool = true
	case silent <= quietWithin:
		a.Status = StatusQuiet
	default:
		a.Status = StatusStopped
	}
	a.SilentMs = silent.Milliseconds()

	if !f.FirstAt.IsZero() {
		end := f.LastAt
		if a.Status == StatusRunning || a.Status == StatusQuiet {
			end = b.now
		}
		if d := end.Sub(f.FirstAt); d > 0 {
			a.DurationMs = d.Milliseconds()
		}
	}

	all, today := b.price(ownResponses(f, main, claimed), count)
	a.All, a.Today = all.spend(), today.spend()
	return a, all, today, true
}

// sortAgents puts agents that are still working first, most recently started
// on top; finished ones follow, most recently active first.
func sortAgents(agents []Agent) {
	rank := func(status string) int {
		switch status {
		case StatusRunning:
			return 0
		case StatusQuiet:
			return 1
		case StatusStopped:
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(agents, func(i, j int) bool {
		ri, rj := rank(agents[i].Status), rank(agents[j].Status)
		if ri != rj {
			return ri < rj
		}
		if ri <= 1 {
			if agents[i].StartedAtMs != agents[j].StartedAtMs {
				return agents[i].StartedAtMs > agents[j].StartedAtMs
			}
		} else if agents[i].LastAtMs != agents[j].LastAtMs {
			return agents[i].LastAtMs > agents[j].LastAtMs
		}
		return agents[i].ID < agents[j].ID
	})
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// activity converts parser activity for display, showing file paths relative
// to the project directory when they are inside it.
func activity(a parse.Activity, cwd string) Activity {
	out := Activity{Kind: a.Kind, Tool: a.Tool, Detail: a.Detail, AtMs: ms(a.At)}
	if a.IsPath {
		out.Detail = relativeTo(cwd, a.Detail)
	}
	return out
}

func relativeTo(cwd, p string) string {
	norm := func(s string) string { return strings.ReplaceAll(s, "\\", "/") }
	base, target := strings.TrimRight(norm(cwd), "/"), norm(p)
	if base == "" || len(target) <= len(base)+1 {
		return p
	}
	if strings.EqualFold(target[:len(base)], base) && target[len(base)] == '/' {
		return target[len(base)+1:]
	}
	return p
}

// ModelLabel turns a model id such as "claude-fable-5-2" into "Fable 5.2".
func ModelLabel(model string) string {
	name := pricing.Normalize(model)
	if name == "" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(name, "claude-"), "-")
	var words, version []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		if p[0] >= '0' && p[0] <= '9' {
			version = append(version, p)
			continue
		}
		if len(version) > 0 {
			// A word after the version (an unusual id): keep the id readable
			// rather than guess at its structure.
			return name
		}
		words = append(words, strings.ToUpper(p[:1])+p[1:])
	}
	if len(words) == 0 {
		return name
	}
	label := strings.Join(words, " ")
	if len(version) > 0 {
		label += " " + strings.Join(version, ".")
	}
	return label
}

// String renders a Spend for logs and the command-line dump.
func (s Spend) String() string {
	return fmt.Sprintf("%d tokens, $%.4f", s.TotalTokens, s.CostUSD)
}
