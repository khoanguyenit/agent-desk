package pricing

import (
	"os"
	"path/filepath"
	"testing"

	"monitor-agent/internal/usage"
)

// These cases are real per-model records Claude Code wrote for its own
// sessions (token counts and the cost it computed). The table must reproduce
// each cost exactly.
func TestCostMatchesClaudeCodeRecords(t *testing.T) {
	table := Default()
	cases := []struct {
		name  string
		model string
		tk    usage.Tokens
		want  int64 // nano-dollars
	}{
		{"fable 5.2, 1h cache writes", "claude-fable-5-2",
			usage.Tokens{Input: 18, Output: 12484, CacheRead: 747858, CacheWrite1h: 87554}, 2_049_939_600},
		{"fable 5.2, second record", "claude-fable-5-2",
			usage.Tokens{Input: 62, Output: 32318, CacheRead: 2076832, CacheWrite1h: 169049}, 4_413_366_400},
		{"fable 5.1", "claude-fable-5-1",
			usage.Tokens{Input: 294, Output: 5383, CacheRead: 799195, CacheWrite1h: 44919}, 1_370_268_750},
		{"opus 5.5, mixed 5m and 1h cache writes", "claude-opus-5-5",
			usage.Tokens{Input: 10870, Output: 11875, CacheRead: 60370, CacheWrite5m: 367070, CacheWrite1h: 378755}, 5_158_444_000},
		{"opus 5.5 with 1M-context marker", "claude-opus-5-5[1m]",
			usage.Tokens{Input: 8, Output: 4316, CacheRead: 207931, CacheWrite1h: 34048}, 400_322_200},
		{"opus 4.8", "claude-opus-4-8",
			usage.Tokens{Input: 10, CacheRead: 164428, CacheWrite1h: 190204}, 1_984_304_000},
		{"sonnet 5.5", "claude-sonnet-5-5",
			usage.Tokens{Input: 12, Output: 1948, CacheRead: 336916, CacheWrite1h: 41014}, 217_251_600},
		{"haiku 5.5, 5m cache write", "claude-haiku-5-5",
			usage.Tokens{Input: 1385, Output: 2967, CacheRead: 3086, CacheWrite5m: 9403}, 2_828_235},
		{"haiku 5.5 with one web search", "claude-haiku-5-5",
			usage.Tokens{Input: 12753, Output: 982, WebSearches: 1}, 11_766_300},
		{"haiku 4.5 with snapshot date", "claude-haiku-4-5-20251001",
			usage.Tokens{Input: 976, Output: 15}, 1_051_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := table.Cost(c.model, false, c.tk)
			if !ok {
				t.Fatalf("model %q not priced", c.model)
			}
			if got != c.want {
				t.Fatalf("cost = %d nUSD, want %d", got, c.want)
			}
		})
	}
}

func TestUnknownModelIsNotGuessed(t *testing.T) {
	table := Default()
	for _, model := range []string{"claude-opus-5-6", "claude-opus-5-5-fast", "gpt-x", "", "<synthetic>"} {
		if _, ok := table.Cost(model, false, usage.Tokens{Input: 1000}); ok {
			t.Errorf("model %q was priced; an unknown model must be reported as unpriced", model)
		}
	}
}

func TestFastModeMultiplier(t *testing.T) {
	table := Default()
	tk := usage.Tokens{Input: 1_000_000, Output: 1_000_000}
	std, _ := table.Cost("claude-opus-5-5", false, tk)
	fast, _ := table.Cost("claude-opus-5-5", true, tk)
	if std != 24*NanoPerUSD || fast != 48*NanoPerUSD {
		t.Fatalf("opus 5.5 standard=%d fast=%d, want $24 and $48", std, fast)
	}
	// A model with no fast tier is priced the same either way.
	a, _ := table.Cost("claude-sonnet-5-5", false, tk)
	b, _ := table.Cost("claude-sonnet-5-5", true, tk)
	if a != b {
		t.Fatalf("sonnet 5.5 has no fast tier but fast=%d standard=%d", b, a)
	}
}

func TestPriceBreakdownAddsUpToCost(t *testing.T) {
	table := Default()
	tk := usage.Tokens{Input: 10870, Output: 11875, CacheRead: 60370, CacheWrite5m: 367070, CacheWrite1h: 378755, WebSearches: 2}
	b, ok := table.Price("claude-opus-5-5", false, tk)
	if !ok {
		t.Fatal("model not priced")
	}
	want := Breakdown{
		Input:      10870 * 4000,
		Output:     11875 * 20000,
		CacheRead:  60370 * 200,
		CacheWrite: 367070*5000 + 378755*8000,
		WebSearch:  2 * 10_000_000,
	}
	if b != want {
		t.Fatalf("breakdown = %+v, want %+v", b, want)
	}
	total, _ := table.Cost("claude-opus-5-5", false, tk)
	if total != b.Total() || total != 5_158_444_000+20_000_000 {
		t.Fatalf("total = %d, breakdown total = %d", total, b.Total())
	}
}

// Corrupt counts must not wrap around into a negative or tiny cost.
func TestCostSaturatesInsteadOfOverflowing(t *testing.T) {
	table := Default()
	huge := usage.Tokens{Output: 1 << 62}
	got, ok := table.Cost("claude-fable-5-2", false, huge)
	if !ok || got != 1<<63-1 {
		t.Fatalf("cost of an absurd count = %d, want it clamped at the maximum", got)
	}
	if got, _ := table.Cost("claude-fable-5-2", false, usage.Tokens{Input: -5, Output: 10}); got != 10*40000 {
		t.Fatalf("a negative count contributed to the cost: %d", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5-5[1m]":       "claude-opus-5-5",
		"claude-haiku-4-5-20251001": "claude-haiku-4-5",
		" Claude-Fable-5-2 ":        "claude-fable-5-2",
		"claude-opus-5-5":           "claude-opus-5-5",
		"claude-opus-4-8-2025":      "claude-opus-4-8-2025",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadOverride(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file keeps the built-in table", func(t *testing.T) {
		table, err := Load(filepath.Join(dir, "absent.json"))
		if err != nil {
			t.Fatal(err)
		}
		if table.Source != "built-in" {
			t.Fatalf("source = %q", table.Source)
		}
	})

	t.Run("override replaces one model and adds another", func(t *testing.T) {
		p := filepath.Join(dir, "pricing.json")
		body := `{"models":{"claude-opus-5-5":{"input":1,"output":2,"cacheRead":0.5},"my-model":{"input":3,"output":4,"cacheRead":0}}}`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		table, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		tk := usage.Tokens{Input: 1_000_000, Output: 1_000_000}
		if got, _ := table.Cost("claude-opus-5-5", false, tk); got != 3*NanoPerUSD {
			t.Fatalf("overridden opus cost = %d", got)
		}
		if got, ok := table.Cost("my-model", false, tk); !ok || got != 7*NanoPerUSD {
			t.Fatalf("added model cost = %d ok=%v", got, ok)
		}
		// Untouched models keep their built-in price.
		if got, _ := table.Cost("claude-sonnet-5-5", false, tk); got != 12*NanoPerUSD {
			t.Fatalf("sonnet cost = %d", got)
		}
	})

	t.Run("invalid override falls back and reports", func(t *testing.T) {
		p := filepath.Join(dir, "broken.json")
		if err := os.WriteFile(p, []byte(`{"models":`), 0o600); err != nil {
			t.Fatal(err)
		}
		table, err := Load(p)
		if err == nil {
			t.Fatal("expected an error for invalid JSON")
		}
		if _, ok := table.Cost("claude-opus-5-5", false, usage.Tokens{Input: 1}); !ok {
			t.Fatal("built-in prices must still work after a bad override")
		}
	})

	t.Run("out-of-range values are rejected", func(t *testing.T) {
		for name, body := range map[string]string{
			"negative price":      `{"models":{"x":{"input":-1,"output":1,"cacheRead":0}}}`,
			"absurd price":        `{"models":{"x":{"input":1e16,"output":1,"cacheRead":0}}}`,
			"absurd cache price":  `{"models":{"x":{"input":1,"output":1,"cacheRead":0,"cacheWrite1h":1e300}}}`,
			"negative multiplier": `{"models":{"x":{"input":1,"output":1,"cacheRead":0,"fastMultiplier":-2}}}`,
			"absurd multiplier":   `{"models":{"x":{"input":1,"output":1,"cacheRead":0,"fastMultiplier":1000000}}}`,
			"negative web search": `{"webSearchUsd":-0.01}`,
			"absurd web search":   `{"webSearchUsd":1e12}`,
		} {
			p := filepath.Join(dir, "bad.json")
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			table, err := Load(p)
			if err == nil {
				t.Errorf("%s: accepted", name)
			}
			if _, ok := table.Cost("claude-opus-5-5", false, usage.Tokens{Input: 1}); !ok || table.Source != "built-in" {
				t.Errorf("%s: the built-in table was not kept", name)
			}
		}
	})

	t.Run("a merged override does not alter the built-in table", func(t *testing.T) {
		p := filepath.Join(dir, "cheap.json")
		if err := os.WriteFile(p, []byte(`{"models":{"claude-opus-5-5":{"input":0,"output":0,"cacheRead":0}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err != nil {
			t.Fatal(err)
		}
		if got, _ := Default().Cost("claude-opus-5-5", false, usage.Tokens{Input: 1_000_000}); got != 4*NanoPerUSD {
			t.Fatalf("built-in opus price changed to %d after loading an override", got)
		}
	})
}
