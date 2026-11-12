// Package usage holds the token counters shared by the transcript parser and
// the price table. It has no dependencies so both can import it.
package usage

// Tokens counts what a set of API responses consumed, split into the
// categories the API bills at different rates.
type Tokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cacheRead"`
	CacheWrite5m int64 `json:"cacheWrite5m"`
	CacheWrite1h int64 `json:"cacheWrite1h"`
	WebSearches  int64 `json:"webSearches"`
	Requests     int64 `json:"requests"`
}

// Add accumulates o into t.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
	t.WebSearches += o.WebSearches
	t.Requests += o.Requests
}

// Sub removes o from t. It is used when a response is re-counted with
// corrected numbers.
func (t *Tokens) Sub(o Tokens) {
	t.Input -= o.Input
	t.Output -= o.Output
	t.CacheRead -= o.CacheRead
	t.CacheWrite5m -= o.CacheWrite5m
	t.CacheWrite1h -= o.CacheWrite1h
	t.WebSearches -= o.WebSearches
	t.Requests -= o.Requests
}

// CacheWrite is the total written to the prompt cache, both lifetimes.
func (t Tokens) CacheWrite() int64 { return t.CacheWrite5m + t.CacheWrite1h }

// Total is every token the API processed. Web searches and the request count
// are not tokens and are left out.
func (t Tokens) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite()
}
