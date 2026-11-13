package monitor

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"monitor-agent/internal/clean"
	"monitor-agent/internal/history"
	"monitor-agent/internal/parse"
	"monitor-agent/internal/pricing"
	"monitor-agent/internal/scan"
	"monitor-agent/internal/usage"
)

const (
	// HistoryMaxDays is the longest stretch History reports.
	HistoryMaxDays = 366
	// historyKeepDays is how far back the history is kept at all.
	historyKeepDays = 400
	dayLayout       = "2006-01-02"
)

// HistoryPart is one project's or one model's share of a day.
type HistoryPart struct {
	// Key identifies the project or model across days.
	Key  string `json:"key"`
	Name string `json:"name"`
	// Path is the project's folder; empty for a model.
	Path           string  `json:"path"`
	CostUSD        float64 `json:"costUsd"`
	Tokens         int64   `json:"tokens"`
	UnpricedTokens int64   `json:"unpricedTokens"`
}

// HistoryDay is everything consumed on one calendar day.
type HistoryDay struct {
	Day   string `json:"day"`
	Spend Spend  `json:"spend"`
	// Sessions is how many sessions were at work that day.
	Sessions int `json:"sessions"`
	// Projects and Models split the day's spend two ways, largest first.
	Projects []HistoryPart `json:"projects"`
	Models   []HistoryPart `json:"models"`
}

// HistoryScan says how far the reading of old transcripts has come.
type HistoryScan struct {
	Running bool `json:"running"`
	Done    int  `json:"done"`
	Total   int  `json:"total"`
}

// History is consumption per day over a stretch that ends today.
type History struct {
	Today string `json:"today"`
	// Days has one entry for every calendar day of the stretch, oldest
	// first, the days on which nothing ran included.
	Days []HistoryDay `json:"days"`
	// FirstDay is the earliest day anything is known about, which may lie
	// before the stretch; empty when nothing is known.
	FirstDay    string      `json:"firstDay"`
	Scan        HistoryScan `json:"scan"`
	PricingAsOf string      `json:"pricingAsOf"`
	// Kept is false when the app keeps no history at all.
	Kept bool `json:"kept"`
}

// HistoryRow is one line of the exported history: what one model consumed in
// one project on one day.
type HistoryRow struct {
	Day     string
	Project string
	Path    string
	Model   string
	Fast    bool
	Tokens  usage.Tokens
	CostUSD float64
	Priced  bool
}

// rowsOf turns a session's usage into the rows the history keeps.
func rowsOf(buckets map[parse.Key]usage.Tokens) []history.Row {
	rows := make([]history.Row, 0, len(buckets))
	for k, tk := range buckets {
		if k.Day == "" {
			// A response without a timestamp belongs to no day.
			continue
		}
		rows = append(rows, history.Row{Day: k.Day, Model: pricing.Normalize(k.Model), Fast: k.Fast, Tokens: tk})
	}
	return rows
}

func projectName(cwd string) (key, name, path string) {
	if cwd == "" {
		return unknownProject, "Không rõ thư mục", ""
	}
	return projectKey(cwd), clean.Text(filepath.Base(filepath.Clean(cwd)), labelRunes), clean.Text(cwd, pathRunes)
}

type partAcc struct {
	name, path string
	acc        acc
}

func partsOf(m map[string]*partAcc) []HistoryPart {
	out := make([]HistoryPart, 0, len(m))
	for key, p := range m {
		out = append(out, HistoryPart{
			Key: key, Name: p.name, Path: p.path,
			CostUSD: dollars(p.acc.cost.Total()), Tokens: p.acc.tokens.Total(), UnpricedTokens: p.acc.unpriced,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// stretch returns the first and the last day of the n days ending today. It
// does not bound n: History does that for what it reports, and the days the
// history is kept for are more than it reports.
func (s *Service) stretch(n int) (from, today string, start time.Time) {
	n = max(n, 1)
	local := s.now().In(s.loc)
	// Noon, so that stepping by a day never lands on the wrong date where a
	// day is 23 or 25 hours long.
	noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, s.loc)
	start = noon.AddDate(0, 0, -(n - 1))
	return start.Format(dayLayout), noon.Format(dayLayout), start
}

// History reports consumption per day for the days ending today, priced with
// the current price table. It is cheap: it reads only what the ledger holds.
func (s *Service) History(days int) History {
	days = min(max(days, 1), HistoryMaxDays)
	from, today, start := s.stretch(days)

	prices, _ := s.prices()
	ledger := s.ledger
	s.scanMu.Lock()
	scanned := s.scan
	s.scanMu.Unlock()

	out := History{Today: today, Days: make([]HistoryDay, 0, days), Scan: scanned, PricingAsOf: prices.AsOf, Kept: ledger != nil}
	type dayAcc struct {
		total    acc
		projects map[string]*partAcc
		models   map[string]*partAcc
		sessions int
	}
	byDay := map[string]*dayAcc{}
	if ledger != nil {
		ledger.Each(func(_ string, e history.Entry) {
			pkey, pname, ppath := projectName(e.Cwd)
			seen := ""
			for _, r := range e.Rows {
				if out.FirstDay == "" || r.Day < out.FirstDay {
					out.FirstDay = r.Day
				}
				if r.Day < from || r.Day > today {
					continue
				}
				d := byDay[r.Day]
				if d == nil {
					d = &dayAcc{projects: map[string]*partAcc{}, models: map[string]*partAcc{}}
					byDay[r.Day] = d
				}
				if r.Day != seen {
					// Rows are in day order, so a session's days come in runs.
					d.sessions++
					seen = r.Day
				}
				cost, ok := prices.Price(r.Model, r.Fast, r.Tokens)
				one := acc{tokens: r.Tokens, cost: cost}
				if !ok {
					one.unpriced = r.Tokens.Total()
				}
				d.total.add(one)
				p := d.projects[pkey]
				if p == nil {
					p = &partAcc{name: pname, path: ppath}
					d.projects[pkey] = p
				}
				p.acc.add(one)
				model := clean.Text(pricing.Normalize(r.Model), labelRunes)
				m := d.models[model]
				if m == nil {
					m = &partAcc{name: ModelLabel(model)}
					d.models[model] = m
				}
				m.acc.add(one)
			}
		})
	}

	for i := range days {
		day := start.AddDate(0, 0, i).Format(dayLayout)
		row := HistoryDay{Day: day, Projects: []HistoryPart{}, Models: []HistoryPart{}}
		if d := byDay[day]; d != nil {
			row.Spend, row.Sessions = d.total.spend(), d.sessions
			row.Projects, row.Models = partsOf(d.projects), partsOf(d.models)
		}
		out.Days = append(out.Days, row)
	}
	return out
}

// HistoryRows lists the same stretch at its finest grain, for export: one row
// per day, project and model, oldest day first.
func (s *Service) HistoryRows(days int) []HistoryRow {
	from, today, _ := s.stretch(min(max(days, 1), HistoryMaxDays))
	prices, _ := s.prices()
	ledger := s.ledger
	if ledger == nil {
		return nil
	}
	type key struct {
		day, project, model string
		fast                bool
	}
	type row struct {
		name, path string
		tokens     usage.Tokens
	}
	sums := map[key]*row{}
	ledger.Each(func(_ string, e history.Entry) {
		pkey, pname, ppath := projectName(e.Cwd)
		for _, r := range e.Rows {
			if r.Day < from || r.Day > today {
				continue
			}
			k := key{r.Day, pkey, clean.Text(pricing.Normalize(r.Model), labelRunes), r.Fast}
			cur := sums[k]
			if cur == nil {
				cur = &row{name: pname, path: ppath}
				sums[k] = cur
			}
			cur.tokens.Add(r.Tokens)
		}
	})
	out := make([]HistoryRow, 0, len(sums))
	for k, r := range sums {
		cost, ok := prices.Cost(k.model, k.fast, r.tokens)
		out = append(out, HistoryRow{
			Day: k.day, Project: r.name, Path: r.path, Model: k.model, Fast: k.fast,
			Tokens: r.tokens, CostUSD: dollars(cost), Priced: ok,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Day != b.Day:
			return a.Day < b.Day
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Model != b.Model:
			return a.Model < b.Model
		}
		return !a.Fast && b.Fast
	})
	return out
}

// SaveHistory writes the history to disk if it changed.
func (s *Service) SaveHistory() error {
	if s.ledger == nil {
		return nil
	}
	return s.ledger.Save()
}

// signature describes the transcript files of a session closely enough to
// tell whether any of them has changed: how many there are, how large, and
// when the latest was written.
func signature(main scan.Transcript, agents []scan.AgentFile) string {
	size, latest := main.Size, main.ModTime
	for _, af := range agents {
		st, err := os.Stat(af.Path)
		if err != nil {
			continue
		}
		size += st.Size()
		if st.ModTime().After(latest) {
			latest = st.ModTime()
		}
	}
	return fmt.Sprintf("%d:%d:%d", len(agents), size, latest.UnixNano())
}

// readSession adds up one session from its transcripts on disk, the way the
// board does for a session it shows. ok is false when the main transcript
// could not be read at all. whole is false when a file was read only in part
// or not at all: what was read is still worth having, but it is not the last
// word on the session.
func readSession(path string, agents []scan.AgentFile, loc *time.Location) (rows []history.Row, cwd string, whole, ok bool) {
	main := parse.NewFile(path, loc, false)
	whole = true
	if err := main.Update(); err != nil {
		if main.Offset() == 0 {
			return nil, "", false, false
		}
		whole = false
	}
	merged := map[parse.Key]usage.Tokens{}
	for k, tk := range main.Usage {
		merged[k] = tk
	}
	claimed := map[string]bool{}
	for _, af := range agents {
		f := parse.NewFile(af.Path, loc, true)
		if err := f.Update(); err != nil {
			whole = false
			if f.Offset() == 0 {
				continue
			}
		}
		for k, tk := range ownResponses(f, main, claimed) {
			cur := merged[k]
			cur.Add(tk)
			merged[k] = cur
		}
	}
	return rowsOf(merged), homeDir(main), whole, true
}

// AwaitBackfill marks the history as being read before the reading has
// begun. The app calls it at start-up, so that a page that asks at once is
// told the history is on its way and not that there is none.
func (s *Service) AwaitBackfill() {
	if s.ledger == nil || s.configErr != nil {
		return
	}
	s.scanMu.Lock()
	s.scan.Running = true
	s.scanMu.Unlock()
}

// Backfill reads into the history the sessions the board does not show: the
// ones from earlier days whose transcripts are still on disk. Sessions already
// read, whose files have not changed since, are skipped, so only the first
// pass on a machine is long. It returns when the pass is done or ctx is
// cancelled, and saves what it found. One pass runs at a time.
func (s *Service) Backfill(ctx context.Context) error {
	if s.ledger == nil || s.configErr != nil {
		return nil
	}
	if !s.filling.TryLock() {
		return nil
	}
	defer s.filling.Unlock()

	all := scan.TranscriptsSince(s.configDir, time.Time{})
	progress := func(done int, running bool) {
		s.scanMu.Lock()
		s.scan = HistoryScan{Running: running, Done: done, Total: len(all)}
		s.scanMu.Unlock()
	}
	progress(0, true)
	defer func() { progress(len(all), false) }()

	cutoff, _, _ := s.stretch(historyKeepDays)
	onDisk := make(map[string]bool, len(all))
	finished := true
	for i, t := range all {
		if ctx.Err() != nil {
			finished = false
			break
		}
		progress(i, true)
		onDisk[t.SessionID] = true
		agents := scan.AgentFiles(t.Path)
		sig := signature(t, agents)
		if s.ledger.Known(t.SessionID, sig) {
			continue
		}
		rows, cwd, whole, ok := readSession(t.Path, agents, s.loc)
		if !ok {
			continue
		}
		if !whole {
			// Not the whole of it: kept, but to be read again next time.
			sig = ""
		}
		kept := rows[:0]
		for _, r := range rows {
			if r.Day >= cutoff {
				kept = append(kept, r)
			}
		}
		s.ledger.Fill(t.SessionID, cwd, sig, kept)
	}
	s.ledger.Prune(cutoff)
	if finished {
		// Only a pass that saw every transcript can say which are gone.
		s.ledger.ForgetEmpty(func(id string) bool { return onDisk[id] })
	}
	return s.ledger.Save()
}

// ModelChoice is a model a new session can be started with.
type ModelChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// OpenUSD is roughly what opening a session costs with this model, at API
	// list price: the system prompt written to the one-hour cache.
	OpenUSD float64 `json:"openUsd"`
}

// openTokens is about how much a new session loads before the first request
// is read: Claude Code's system prompt and tool definitions.
const openTokens = 30_000

// family splits a normalized model id into its name and version numbers:
// "claude-opus-5-5" gives "opus" and [5 5]. ok is false for an id of another
// shape.
func family(model string) (name string, version []int, ok bool) {
	var words []string
	for _, p := range strings.Split(strings.TrimPrefix(model, "claude-"), "-") {
		if p == "" {
			return "", nil, false
		}
		if p[0] >= '0' && p[0] <= '9' {
			n := 0
			for _, c := range p {
				if c < '0' || c > '9' || n > 1_000_000 {
					return "", nil, false
				}
				n = n*10 + int(c-'0')
			}
			version = append(version, n)
			continue
		}
		if len(version) > 0 {
			return "", nil, false
		}
		words = append(words, p)
	}
	if len(words) == 0 || len(version) == 0 {
		return "", nil, false
	}
	return strings.Join(words, "-"), version, true
}

func newer(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

// ModelChoices lists the models offered for a new session: of each family in
// the price table, the newest one whose prices are verified, which means
// Claude Code on some machine has been seen to use it at those prices. The
// most expensive comes first.
func (s *Service) ModelChoices() []ModelChoice {
	prices, _ := s.prices()

	type best struct {
		id      string
		version []int
		rates   pricing.Rates
	}
	newest := map[string]best{}
	for _, id := range prices.Models() {
		r, _ := prices.Lookup(id)
		name, version, ok := family(id)
		if !ok || !r.Verified {
			continue
		}
		if cur, have := newest[name]; !have || newer(version, cur.version) {
			newest[name] = best{id: id, version: version, rates: r}
		}
	}
	out := make([]ModelChoice, 0, len(newest))
	for _, b := range newest {
		out = append(out, ModelChoice{
			ID: b.id, Label: ModelLabel(b.id),
			OpenUSD: dollars(openTokens * b.rates.CacheWrite1h),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OpenUSD != out[j].OpenUSD {
			return out[i].OpenUSD > out[j].OpenUSD
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// csvCell guards a text cell against being read as a formula by a
// spreadsheet. A folder can be named anything, "=cmd|..." included, and a
// spreadsheet that opens the export would run it.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// WriteHistoryCSV writes rows as a CSV file a spreadsheet opens directly:
// UTF-8 with a byte-order mark, one row per day, project and model. Costs
// are dollars at API list price; a model without a price has none.
func WriteHistoryCSV(w io.Writer, rows []HistoryRow) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	out := csv.NewWriter(w)
	out.UseCRLF = true
	if err := out.Write([]string{
		"Ngày", "Project", "Thư mục", "Model", "Fast",
		"Input", "Output", "Cache đọc", "Cache ghi 5 phút", "Cache ghi 1 giờ",
		"Web search", "Lượt gọi API", "Tổng token", "Chi phí (USD, giá API)",
	}); err != nil {
		return err
	}
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	for _, r := range rows {
		fast, cost := "", ""
		if r.Fast {
			fast = "fast"
		}
		if r.Priced {
			cost = strconv.FormatFloat(r.CostUSD, 'f', 6, 64)
		}
		tk := r.Tokens
		if err := out.Write([]string{
			r.Day, csvCell(r.Project), csvCell(r.Path), csvCell(r.Model), fast,
			n(tk.Input), n(tk.Output), n(tk.CacheRead), n(tk.CacheWrite5m), n(tk.CacheWrite1h),
			n(tk.WebSearches), n(tk.Requests), n(tk.Total()), cost,
		}); err != nil {
			return err
		}
	}
	out.Flush()
	return out.Error()
}
