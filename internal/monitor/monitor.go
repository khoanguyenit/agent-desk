// Package monitor assembles the view the UI shows: every running Claude Code
// session grouped by project, its subagents, what each is doing, and what all
// of it has consumed and cost.
package monitor

import (
	"fmt"
	"maps"
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
	"monitor-agent/internal/history"
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

// What Claude Code records as the state of a session. A session is busy while
// a turn runs, idle when the turn is over, and waiting when the turn has
// stopped for the user: a question to answer, an action to approve. Other
// words are passed through as they are.
const (
	SessionBusy    = "busy"
	SessionIdle    = "idle"
	SessionWaiting = "waiting"
)

// Agent states.
const (
	StatusRunning = "running"
	StatusQuiet   = "quiet"
	StatusStopped = "stopped"
	StatusDone    = "done"
	// StatusLimited: the agent's last answer was a refusal for a usage limit
	// that has not lifted. It is not working, and will not until it does.
	StatusLimited = "limited"
)

// Limit is a usage limit a session or agent ran into and that has not
// lifted: Claude Code will not reach the model for it until it does.
type Limit struct {
	// Kind is Claude Code's name for the limit, such as "five_hour" or
	// "seven_day"; empty when the transcript does not say.
	Kind string `json:"kind"`
	// ResetsAtMs is when it lifts, or zero when the transcript does not say.
	ResetsAtMs int64 `json:"resetsAtMs"`
	// AtMs is when the refusal was written.
	AtMs int64 `json:"atMs"`
}

// LimitGroup is one usage limit that stops work on the page, and how much it
// stops. Sessions on one account that ran into the same limit share it.
type LimitGroup struct {
	Kind       string `json:"kind"`
	ResetsAtMs int64  `json:"resetsAtMs"`
	Sessions   int    `json:"sessions"`
	Agents     int    `json:"agents"`
}

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
	// Limit is set while the agent is held by a usage limit; its status is
	// then StatusLimited.
	Limit *Limit `json:"limit"`
	// unlimited is the status the agent has when its limit is found to
	// have lifted.
	unlimited string
}

// Session is one running Claude Code process.
type Session struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Title  string `json:"title"`
	PID    int    `json:"pid"`
	Status string `json:"status"`
	// WaitingFor is what a waiting session says it waits for, in Claude
	// Code's own words; empty for any other status.
	WaitingFor    string   `json:"waitingFor"`
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
	// Limit is set while the main thread is held by a usage limit.
	Limit *Limit `json:"limit"`
	// account is the Claude account the session runs under, to tell which
	// sessions share a limit. It never leaves this package.
	account string
}

// ClosedSession is a session that has ended but was at work today. Its
// transcript is still on disk, so what it spent is known, and it stays in the
// totals: closing a project must not make its cost disappear from the day.
type ClosedSession struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Model      string `json:"model"`
	ModelLabel string `json:"modelLabel"`
	GitBranch  string `json:"gitBranch"`
	// EndedAtMs is when the last thing was written to its transcript.
	EndedAtMs int64 `json:"endedAtMs"`
	// Agents is how many subagents it ran; their spend is in All and Today.
	Agents int   `json:"agents"`
	All    Spend `json:"all"`
	Today  Spend `json:"today"`
}

// Project groups the sessions that share a working directory. All and Today
// take in the sessions that are running and the ones that closed today.
type Project struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Sessions []Session `json:"sessions"`
	// Closed lists the sessions of this project that ended but were at work
	// today, most recently ended first.
	Closed []ClosedSession `json:"closed"`
	All    Spend           `json:"all"`
	Today  Spend           `json:"today"`
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
		Closed:   []ClosedSession{},
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
	Projects     int `json:"projects"`
	Sessions     int `json:"sessions"`
	BusySessions int `json:"busySessions"`
	// ClosedSessions counts sessions that ended but were at work today.
	ClosedSessions int `json:"closedSessions"`
	Agents         int `json:"agents"`
	RunningAgents  int `json:"runningAgents"`
	QuietAgents    int `json:"quietAgents"`
}

// Snapshot is everything the UI renders.
type Snapshot struct {
	GeneratedAtMs int64  `json:"generatedAtMs"`
	Today         string `json:"today"`
	ConfigDir     string `json:"configDir"`
	Counts        Counts `json:"counts"`
	// All and TodaySpend cover every session on the page: the ones running
	// now and the ones that closed today. ClosedAll and ClosedToday are the
	// part of them that comes from the closed ones.
	All           Spend      `json:"all"`
	TodaySpend    Spend      `json:"todaySpend"`
	ClosedAll     Spend      `json:"closedAll"`
	ClosedToday   Spend      `json:"closedToday"`
	Projects      []Project  `json:"projects"`
	Models        []ModelRow `json:"models"`
	PricingAsOf   string     `json:"pricingAsOf"`
	PricingSource string     `json:"pricingSource"`
	PricingPath   string     `json:"pricingPath"`
	Warnings      []string   `json:"warnings"`
	// Limits are the usage limits holding work on the page, soonest to lift
	// first.
	Limits []LimitGroup `json:"limits"`
}

// Options configures a Service. Zero values select the real environment.
type Options struct {
	ConfigDir   string
	PricingPath string
	Alive       scan.AliveFunc
	Now         func() time.Time
	Location    *time.Location
	// History, when given, is told what every session on the board consumed
	// day by day, and is what History reports from. Without it no history is
	// kept.
	History *history.Ledger
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

	// ended is the list of transcripts written to today whose session is not
	// running: the sessions that closed today. It is made again every so
	// often, and at once when the set of running sessions or the day changes.
	ended    []endedTranscript
	endedAt  time.Time
	endedFor string

	// usages holds what each session's transcripts add up to, by the path of
	// its main transcript, for as long as none of them has changed. lists
	// holds the subagent transcripts found for a session.
	usages map[string]*sessionUsage
	lists  map[string]*agentList
	// closed is the sessions listed as closed in the latest snapshot, by id.
	// Only those can be looked into after they have ended.
	closed map[string]closedRef
	trail  trailCache
	// priced has a lock of its own: a report on the history needs the price
	// table and must not wait for a scan of the disk to finish.
	pricedMu sync.Mutex
	priced   priceCache

	ledger *history.Ledger
	scanMu sync.Mutex
	scan   HistoryScan
	// filling is held by the one pass over old transcripts that may run.
	filling sync.Mutex
}

// closedRef is where a session that closed today left its transcripts.
type closedRef struct {
	path   string
	cwd    string
	agents []scan.AgentFile
}

// priceCache is the price table as last loaded, with what the override file
// looked like then.
type priceCache struct {
	loaded bool
	mod    time.Time
	size   int64
	table  pricing.Table
	err    error
}

// sessionUsage is what the transcripts of one session add up to. Working it
// out walks every response of every agent, so it is kept for as long as none
// of the files has changed.
type sessionUsage struct {
	// paths are the agent transcripts it was made from, in order; versions
	// has the main transcript first, then one for each of them.
	paths    []string
	versions []int64
	// agents is what each agent consumed without the responses that are
	// counted elsewhere; merged is the main thread and all of them together.
	agents []map[parse.Key]usage.Tokens
	merged map[parse.Key]usage.Tokens
}

func (u *sessionUsage) matches(main *parse.File, paths []string, agents []*parse.File) bool {
	if u == nil || len(u.versions) != len(agents)+1 || u.versions[0] != main.Version() || !slices.Equal(u.paths, paths) {
		return false
	}
	for i, f := range agents {
		if u.versions[i+1] != f.Version() {
			return false
		}
	}
	return true
}

// agentList is the subagent transcripts of a session as last listed.
type agentList struct {
	files []scan.AgentFile
	at    time.Time
	// active is true when one of them was still at work at the last look.
	active bool
}

// listEvery is how long the list of a session's agents is reused while
// nothing is at work in it. New agents appear only when something runs, so a
// session at rest does not have its folders walked on every refresh.
const listEvery = 10 * time.Second

type endedTranscript struct {
	scan.Transcript
	agents []scan.AgentFile
}

// endedRescanEvery is how often the folders are listed again for sessions
// that have ended. Listing costs more than reading what is already known.
const endedRescanEvery = 15 * time.Second

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
		usages:      map[string]*sessionUsage{},
		lists:       map[string]*agentList{},
		closed:      map[string]closedRef{},
		ledger:      o.History,
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
	// responded is, per account, when any of its sessions last got a real
	// answer from the model.
	responded map[string]time.Time
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
		Limits:        []LimitGroup{},
	}

	prices, err := s.prices()
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
		s:         s,
		now:       now,
		today:     snap.Today,
		prices:    prices,
		models:    map[string]*modelAcc{},
		tallied:   map[string]bool{},
		responded: map[string]time.Time{},
	}

	live := make(map[string]bool, len(sessions))
	ids := make([]string, 0, len(sessions))
	for _, rs := range sessions {
		live[rs.SessionID] = true
		ids = append(ids, rs.SessionID)
	}
	slices.Sort(ids)
	if mark := snap.Today + "|" + strings.Join(ids, ","); mark != s.endedFor || now.Sub(s.endedAt) >= endedRescanEvery || now.Before(s.endedAt) {
		local := now.In(s.loc)
		midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
		s.ended = s.ended[:0]
		for _, t := range scan.TranscriptsSince(s.configDir, midnight) {
			if !live[t.SessionID] {
				s.ended = append(s.ended, endedTranscript{t, scan.AgentFiles(t.Path)})
			}
		}
		s.endedAt, s.endedFor = now, mark
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

	// Sessions that closed today are added to the totals, and listed under
	// their project, from the transcripts they left behind.
	var closedAll, closedToday acc
	clear(s.closed)
	for _, e := range s.ended {
		if live[e.SessionID] || b.tallied[e.Path] {
			continue
		}
		cs, cwd, all, today, ok := b.closed(e)
		if !ok {
			continue
		}
		b.tallied[e.Path] = true
		s.closed[e.SessionID] = closedRef{path: e.Path, cwd: cwd, agents: e.agents}
		key := projectKey(cwd)
		if cwd == "" {
			// Nothing in the transcript says where it ran: such sessions
			// are kept together rather than their cost dropped.
			key = unknownProject
		}
		p, found := byProject[key]
		if !found {
			p = &projectAcc{project: Project{
				Name:     clean.Text(filepath.Base(filepath.Clean(cwd)), labelRunes),
				Path:     clean.Text(cwd, pathRunes),
				Sessions: []Session{},
				dir:      cwd,
			}}
			if cwd == "" {
				p.project.Name, p.project.Path = "Không rõ thư mục", ""
			}
			byProject[key] = p
			order = append(order, key)
		}
		p.project.Closed = append(p.project.Closed, cs)
		p.all.add(all)
		p.today.add(today)
		total.add(all)
		totalToday.add(today)
		closedAll.add(all)
		closedToday.add(today)
		snap.Counts.ClosedSessions++
	}
	snap.ClosedAll, snap.ClosedToday = closedAll.spend(), closedToday.spend()

	// Usage limits are settled once every session has been heard, the closed
	// ones too: any of them may show that its account was answered since.
	settle := make([]*Project, 0, len(byProject))
	for _, p := range byProject {
		settle = append(settle, &p.project)
	}
	snap.Limits = b.settleLimits(settle, &snap.Counts)

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
	for path := range s.usages {
		if _, ok := s.files[path]; !ok {
			delete(s.usages, path)
		}
	}
	for path := range s.lists {
		if _, ok := s.files[path]; !ok {
			delete(s.lists, path)
		}
	}

	slices.Sort(order)
	for _, key := range order {
		p := byProject[key]
		p.project.All, p.project.Today = p.all.spend(), p.today.spend()
		if p.project.Closed == nil {
			p.project.Closed = []ClosedSession{}
		}
		sort.SliceStable(p.project.Closed, func(i, j int) bool {
			return p.project.Closed[i].EndedAtMs > p.project.Closed[j].EndedAtMs
		})
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

// unknownProject is the key under which sessions with no known folder are
// kept. It cannot be the key of a real folder.
const unknownProject = "?unknown"

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

// usage returns what a session's transcripts add up to: each agent's own
// responses, and everything together. It is worked out again only when one of
// the files has changed since the last time; fresh says that it was.
func (b *build) usage(mainPath string, main *parse.File, paths []string, agents []*parse.File) (u *sessionUsage, fresh bool) {
	if u = b.s.usages[mainPath]; u.matches(main, paths, agents) {
		return u, false
	}
	u = &sessionUsage{
		paths:    slices.Clone(paths),
		versions: make([]int64, 0, len(agents)+1),
		agents:   make([]map[parse.Key]usage.Tokens, len(agents)),
		merged:   maps.Clone(main.Usage),
	}
	u.versions = append(u.versions, main.Version())
	claimed := map[string]bool{}
	for i, f := range agents {
		u.versions = append(u.versions, f.Version())
		own := ownResponses(f, main, claimed)
		u.agents[i] = own
		for k, tk := range own {
			cur := u.merged[k]
			cur.Add(tk)
			u.merged[k] = cur
		}
	}
	b.s.usages[mainPath] = u
	return u, true
}

// agentFiles lists the subagent transcripts of the session whose main
// transcript is path. The folders are walked again when the session is busy,
// when one of its agents was still at work, or when the list has aged.
func (b *build) agentFiles(path string, busy bool) *agentList {
	l := b.s.lists[path]
	if l == nil {
		l = &agentList{}
		b.s.lists[path] = l
	} else if !busy && !l.active && !b.now.Before(l.at) && b.now.Sub(l.at) < listEvery {
		return l
	}
	l.files, l.at = scan.AgentFiles(path), b.now
	return l
}

// record hands a session's consumption to the history, when one is kept.
func (b *build) record(sessionID, cwd string, merged map[parse.Key]usage.Tokens) {
	if b.s.ledger != nil {
		b.s.ledger.Record(sessionID, cwd, rowsOf(merged))
	}
}

// prices returns the price table, read again only when the override file has
// changed.
func (s *Service) prices() (pricing.Table, error) {
	s.pricedMu.Lock()
	defer s.pricedMu.Unlock()
	var mod time.Time
	size := int64(-1)
	if s.pricingPath != "" {
		if st, err := os.Stat(s.pricingPath); err == nil {
			mod, size = st.ModTime(), st.Size()
		}
	}
	c := &s.priced
	if c.loaded && c.size == size && c.mod.Equal(mod) {
		return c.table, c.err
	}
	c.table, c.err = pricing.Load(s.pricingPath)
	c.loaded, c.mod, c.size = true, mod, size
	return c.table, c.err
}

// closed adds up a session that has ended, from the transcripts it left on
// disk. ok is false when the session did nothing today: such a one is neither
// listed nor counted, and leaves no trace in the totals per model.
func (b *build) closed(e endedTranscript) (cs ClosedSession, cwd string, all, today acc, ok bool) {
	f, read := b.file(e.Path, false)
	if !read {
		return cs, "", all, today, false
	}
	var paths []string
	var states []*parse.File
	for _, af := range e.agents {
		fa, read := b.file(af.Path, true)
		if !read {
			continue
		}
		paths = append(paths, af.Path)
		states = append(states, fa)
	}
	agents := len(states)
	// A closed session may be the one that shows its account was answered
	// after a limit, so it is heard like a running one.
	b.heard(f.Account, f.RespondedAt)
	for _, st := range states {
		b.heard(f.Account, st.RespondedAt)
	}
	u, fresh := b.usage(e.Path, f, paths, states)
	if fresh {
		// Before anything else: a session whose last lines were written
		// just before midnight is not at work today, and is not listed, but
		// what it consumed yesterday must still reach the history.
		b.record(e.SessionID, homeDir(f), u.merged)
	}
	parts := append([]map[parse.Key]usage.Tokens{f.Usage}, u.agents...)
	atWorkToday := false
	for _, part := range parts {
		for k := range part {
			atWorkToday = atWorkToday || k.Day == b.today
		}
	}
	if !atWorkToday {
		return cs, "", all, today, false
	}
	for _, part := range parts {
		a, t := b.price(part, true)
		all.add(a)
		today.add(t)
	}

	ended := f.LastAt
	if ended.IsZero() {
		ended = f.ModTime
	}
	cs = ClosedSession{
		ID: e.SessionID,
		// The title Claude Code gave it, and nothing in its place when it
		// has none: the only other name it has is a prompt, which the
		// switch that hides contents is there to keep off the screen.
		Title:      f.Title,
		Model:      f.Model,
		ModelLabel: ModelLabel(f.Model),
		GitBranch:  f.GitBranch,
		EndedAtMs:  ms(ended),
		Agents:     agents,
		All:        all.spend(),
		Today:      today.spend(),
	}
	return cs, homeDir(f), all, today, true
}

// homeDir is the folder a session that has ended belongs to: the one Claude
// Code filed its transcript under, which is the folder it started in. A
// session can go on in another one, a subfolder for instance, and its last
// lines then name that; it still belongs to the project it started in, and
// can be resumed only from there. Of the two folders its lines name, the one
// the transcript is filed under wins; when neither is, the first.
func homeDir(f *parse.File) string {
	filed := filepath.Base(filepath.Dir(f.Path))
	for _, dir := range []string{f.Cwd, f.FirstCwd} {
		if dir != "" && sameSlug(scan.Slug(dir), filed) {
			return dir
		}
	}
	if f.FirstCwd != "" {
		return f.FirstCwd
	}
	return f.Cwd
}

// sameSlug reports whether two project folder names are the same. Where
// capitals do not tell two folders apart they do not tell two spellings of
// one folder apart either: "d:\x" and "D:\x" are filed under the same folder.
func sameSlug(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
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
		Pulse:           []int{},
		RecordedCostUSD: -1,
	}
	if rs.Status == SessionWaiting {
		sess.WaitingFor = clean.Text(rs.WaitingFor, labelRunes)
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
	sess.account = f.Account
	b.heard(f.Account, f.RespondedAt)
	sess.Limit = b.limit(f)
	sess.Title = f.Title
	sess.GitBranch = f.GitBranch
	sess.LastAtMs = ms(f.LastAt)
	sess.Model = f.Model
	sess.ModelLabel = ModelLabel(f.Model)
	sess.ContextTokens = f.ContextTokens
	sess.LastPrompt = f.LastPrompt
	sess.Activity = activity(f.Last, rs.Cwd)
	if rs.Status == "busy" {
		// The pulse is a sign of life. It is drawn only for what is still
		// working, so it is not sent for anything else.
		sess.Pulse = f.Pulse(b.now, PulseMinutes)
	}
	if f.HasRecordedCost {
		sess.RecordedCostUSD = f.RecordedCostUSD
	}

	own, ownToday := b.price(f.Usage, count)
	sess.Own = own.spend()
	all.add(own)
	today.add(ownToday)

	list := b.agentFiles(path, rs.Status == "busy")
	var found []scan.AgentFile
	var paths []string
	var states []*parse.File
	for _, af := range list.files {
		fa, ok := b.file(af.Path, true)
		if !ok {
			continue
		}
		found = append(found, af)
		paths = append(paths, af.Path)
		states = append(states, fa)
	}
	u, fresh := b.usage(path, f, paths, states)
	for i, af := range found {
		a, aAll, aToday := b.agent(af, states[i], rs.Cwd, u.agents[i], count)
		b.heard(f.Account, states[i].RespondedAt)
		sess.Agents = append(sess.Agents, a)
		if a.Status == StatusRunning || a.Status == StatusQuiet {
			sess.ActiveAgents++
		}
		all.add(aAll)
		today.add(aToday)
	}
	list.active = sess.ActiveAgents > 0
	sortAgents(sess.Agents)
	if count && fresh {
		b.record(rs.SessionID, rs.Cwd, u.merged)
	}

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

// agent describes one subagent from its transcript f. own is what it consumed
// without the responses counted elsewhere; see ownResponses.
func (b *build) agent(af scan.AgentFile, f *parse.File, cwd string, own map[parse.Key]usage.Tokens, count bool) (Agent, acc, acc) {
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
		Pulse:         []int{},
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
	// Held by a usage limit, the agent is neither working nor given up on:
	// it is waiting for the limit, and says so. Which status it would have
	// otherwise is kept, for when the limit turns out to have lifted.
	if l := b.limit(f); l != nil && !ended {
		a.Limit, a.unlimited, a.Status = l, a.Status, StatusLimited
		a.AwaitingTool = false
	}
	a.SilentMs = silent.Milliseconds()
	if a.Status == StatusRunning || a.Status == StatusQuiet {
		a.Pulse = f.Pulse(b.now, PulseMinutes)
	}

	if !f.FirstAt.IsZero() {
		end := f.LastAt
		if a.Status == StatusRunning || a.Status == StatusQuiet {
			end = b.now
		}
		if d := end.Sub(f.FirstAt); d > 0 {
			a.DurationMs = d.Milliseconds()
		}
	}

	all, today := b.price(own, count)
	a.All, a.Today = all.spend(), today.spend()
	return a, all, today
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

// heard notes when a session of an account last got a real answer from the
// model. Sessions whose account is not known are not grouped with any other.
func (b *build) heard(account string, at time.Time) {
	if account == "" || at.IsZero() {
		return
	}
	if at.After(b.responded[account]) {
		b.responded[account] = at
	}
}

// limit returns the usage limit f last ran into, unless the time it named
// for lifting has come.
func (b *build) limit(f *parse.File) *Limit {
	l := f.Limit
	if l == nil || (!l.ResetsAt.IsZero() && !b.now.Before(l.ResetsAt)) {
		return nil
	}
	return &Limit{Kind: l.Kind, ResetsAtMs: ms(l.ResetsAt), AtMs: ms(l.At)}
}

// settleLimits drops the limits that have lifted for the account they belong
// to, and gathers the rest. A limit is the account's, not one session's: when
// any session on the same account has been answered since it was hit, it has
// lifted for all of them, whether its time came or the user reset it. A
// session on another account says nothing about it.
func (b *build) settleLimits(projects []*Project, counts *Counts) []LimitGroup {
	lifted := func(account string, l *Limit) bool {
		at, ok := b.responded[account]
		return account != "" && ok && at.UnixMilli() > l.AtMs
	}
	type key struct {
		kind string
		at   int64
	}
	groups := map[key]*LimitGroup{}
	group := func(l *Limit) *LimitGroup {
		k := key{l.Kind, l.ResetsAtMs}
		g, ok := groups[k]
		if !ok {
			g = &LimitGroup{Kind: l.Kind, ResetsAtMs: l.ResetsAtMs}
			groups[k] = g
		}
		return g
	}
	for _, p := range projects {
		for i := range p.Sessions {
			s := &p.Sessions[i]
			if s.Limit != nil && lifted(s.account, s.Limit) {
				s.Limit = nil
			}
			if s.Limit != nil && !s.Duplicate {
				group(s.Limit).Sessions++
			}
			for j := range s.Agents {
				a := &s.Agents[j]
				if a.Limit == nil {
					continue
				}
				if !lifted(s.account, a.Limit) {
					if !s.Duplicate {
						group(a.Limit).Agents++
					}
					continue
				}
				a.Limit, a.Status = nil, a.unlimited
				if a.Status != StatusRunning && a.Status != StatusQuiet {
					continue
				}
				s.ActiveAgents++
				if s.Duplicate {
					continue
				}
				if a.Status == StatusRunning {
					counts.RunningAgents++
				} else {
					counts.QuietAgents++
				}
			}
		}
	}
	out := make([]LimitGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		// Soonest to lift first; one whose time is unknown last.
		ai, aj := out[i].ResetsAtMs, out[j].ResetsAtMs
		if (ai == 0) != (aj == 0) {
			return aj == 0
		}
		if ai != aj {
			return ai < aj
		}
		return out[i].Kind < out[j].Kind
	})
	return out
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
