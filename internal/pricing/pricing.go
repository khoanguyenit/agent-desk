// Package pricing turns token counts into an API-list-price cost.
//
// All arithmetic is done in whole nano-dollars (1e-9 USD) per token, so sums
// over millions of tokens stay exact and match the figures Claude Code records
// for itself. Every published rate is a whole number of nano-dollars per token.
package pricing

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	"monitor-agent/internal/fsx"
	"monitor-agent/internal/usage"
)

//go:embed pricing.json
var defaultJSON []byte

// NanoPerUSD converts between the integer unit used here and dollars.
const NanoPerUSD = 1_000_000_000

const (
	// Limits on what a price file may say. They are far above any real price
	// and exist so that a typo cannot overflow the arithmetic.
	maxUSDPerMTok   = 10_000
	maxUSDPerSearch = 1_000
	maxMultiplier   = 100
	maxFileBytes    = 1 << 20
)

// Rates are nano-dollars per token for one model.
type Rates struct {
	Input          int64
	Output         int64
	CacheRead      int64
	CacheWrite5m   int64
	CacheWrite1h   int64
	FastMultiplier int64
	// Verified reports that these rates reproduce costs Claude Code recorded.
	Verified bool
}

// Breakdown is a cost in nano-dollars split by what was paid for.
type Breakdown struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	WebSearch  int64
}

// Total is the whole cost.
func (b Breakdown) Total() int64 {
	return addSat(addSat(addSat(b.Input, b.Output), addSat(b.CacheRead, b.CacheWrite)), b.WebSearch)
}

// Add accumulates o into b.
func (b *Breakdown) Add(o Breakdown) {
	b.Input = addSat(b.Input, o.Input)
	b.Output = addSat(b.Output, o.Output)
	b.CacheRead = addSat(b.CacheRead, o.CacheRead)
	b.CacheWrite = addSat(b.CacheWrite, o.CacheWrite)
	b.WebSearch = addSat(b.WebSearch, o.WebSearch)
}

// Table is a set of model rates.
type Table struct {
	AsOf      string
	Source    string
	webSearch int64
	models    map[string]Rates
}

type modelJSON struct {
	Input          float64  `json:"input"`
	Output         float64  `json:"output"`
	CacheRead      float64  `json:"cacheRead"`
	CacheWrite5m   *float64 `json:"cacheWrite5m"`
	CacheWrite1h   *float64 `json:"cacheWrite1h"`
	FastMultiplier int64    `json:"fastMultiplier"`
	Verified       bool     `json:"verified"`
}

type fileJSON struct {
	AsOf         string               `json:"asOf"`
	WebSearchUSD *float64             `json:"webSearchUsd"`
	Models       map[string]modelJSON `json:"models"`
}

// perMTok converts dollars per million tokens to nano-dollars per token. The
// value is checked before conversion: converting an out-of-range float to an
// integer is not defined.
func perMTok(name string, usd float64) (int64, error) {
	if math.IsNaN(usd) || usd < 0 || usd > maxUSDPerMTok {
		return 0, fmt.Errorf("%s must be between 0 and %d USD per million tokens", name, maxUSDPerMTok)
	}
	return int64(math.Round(usd * 1000)), nil
}

func (m modelJSON) rates() (Rates, error) {
	r := Rates{FastMultiplier: m.FastMultiplier, Verified: m.Verified}
	if m.FastMultiplier < 0 || m.FastMultiplier > maxMultiplier {
		return Rates{}, fmt.Errorf("fastMultiplier must be between 0 and %d", maxMultiplier)
	}
	w5m, w1h := m.Input*1.25, m.Input*2
	if m.CacheWrite5m != nil {
		w5m = *m.CacheWrite5m
	}
	if m.CacheWrite1h != nil {
		w1h = *m.CacheWrite1h
	}
	for _, f := range []struct {
		name string
		usd  float64
		dst  *int64
	}{
		{"input", m.Input, &r.Input},
		{"output", m.Output, &r.Output},
		{"cacheRead", m.CacheRead, &r.CacheRead},
		{"cacheWrite5m", w5m, &r.CacheWrite5m},
		{"cacheWrite1h", w1h, &r.CacheWrite1h},
	} {
		v, err := perMTok(f.name, f.usd)
		if err != nil {
			return Rates{}, err
		}
		*f.dst = v
	}
	return r, nil
}

func (t *Table) merge(raw []byte) error {
	var f fileJSON
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	for name, m := range f.Models {
		r, err := m.rates()
		if err != nil {
			return fmt.Errorf("model %q: %w", name, err)
		}
		t.models[Normalize(name)] = r
	}
	if f.WebSearchUSD != nil {
		v := *f.WebSearchUSD
		if math.IsNaN(v) || v < 0 || v > maxUSDPerSearch {
			return fmt.Errorf("webSearchUsd must be between 0 and %d", maxUSDPerSearch)
		}
		t.webSearch = int64(math.Round(v * NanoPerUSD))
	}
	if f.AsOf != "" {
		t.AsOf = f.AsOf
	}
	return nil
}

// builtIn parses the embedded table once. Callers get a copy of the model
// map, so merging an override never alters it.
var builtIn = sync.OnceValue(func() Table {
	t := Table{Source: "built-in", models: map[string]Rates{}}
	if err := t.merge(defaultJSON); err != nil {
		// The embedded file is part of the build; a bad one is a programming error.
		panic("pricing: embedded table is invalid: " + err.Error())
	}
	return t
})

// Default returns the built-in price table.
func Default() Table {
	t := builtIn()
	t.models = maps.Clone(t.models)
	return t
}

// Load returns the built-in table with the entries of overridePath laid on
// top. A missing file is not an error. If the file is unreadable or invalid
// the built-in table is returned together with the error, so the caller can
// keep working and show the problem.
func Load(overridePath string) (Table, error) {
	t := Default()
	if overridePath == "" {
		return t, nil
	}
	raw, err := fsx.ReadSmall(overridePath, maxFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, fmt.Errorf("read %s: %w", overridePath, err)
	}
	merged := Default()
	if err := merged.merge(raw); err != nil {
		return t, fmt.Errorf("parse %s: %w", overridePath, err)
	}
	merged.Source = overridePath
	return merged, nil
}

// Normalize maps the model names found in transcripts onto table keys: it
// drops a context-window marker such as "[1m]" and a trailing snapshot date
// such as "-20251001".
func Normalize(model string) string {
	if i := strings.IndexByte(model, '['); i >= 0 {
		model = model[:i]
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if n := len(model); n > 9 && model[n-9] == '-' && allDigits(model[n-8:]) {
		model = model[:n-9]
	}
	return model
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// Models lists the ids of every model in the table, in alphabetical order.
func (t Table) Models() []string {
	out := make([]string, 0, len(t.models))
	for id := range t.models {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Lookup returns the rates for a model. Only an exact match after Normalize
// counts: guessing from a similar name would silently misprice a new model.
func (t Table) Lookup(model string) (Rates, bool) {
	r, ok := t.models[Normalize(model)]
	return r, ok
}

// Price costs tk for model, split by category. ok is false when the model is
// not in the table; the caller should then report the tokens as unpriced.
// Sums saturate instead of wrapping, so a corrupt count cannot turn into a
// negative cost.
func (t Table) Price(model string, fast bool, tk usage.Tokens) (Breakdown, bool) {
	r, ok := t.Lookup(model)
	if !ok {
		return Breakdown{}, false
	}
	mult := int64(1)
	if fast && r.FastMultiplier > 1 {
		mult = r.FastMultiplier
	}
	return Breakdown{
		Input:      mulSat(mulSat(tk.Input, r.Input), mult),
		Output:     mulSat(mulSat(tk.Output, r.Output), mult),
		CacheRead:  mulSat(mulSat(tk.CacheRead, r.CacheRead), mult),
		CacheWrite: mulSat(addSat(mulSat(tk.CacheWrite5m, r.CacheWrite5m), mulSat(tk.CacheWrite1h, r.CacheWrite1h)), mult),
		WebSearch:  mulSat(tk.WebSearches, t.webSearch),
	}, true
}

// Cost is the total of Price in nano-dollars.
func (t Table) Cost(model string, fast bool, tk usage.Tokens) (nanoUSD int64, ok bool) {
	b, ok := t.Price(model, fast, tk)
	return b.Total(), ok
}

// mulSat multiplies two non-negative quantities, clamping at the int64
// maximum. A negative operand, which no valid count or rate is, yields zero.
func mulSat(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func addSat(a, b int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
