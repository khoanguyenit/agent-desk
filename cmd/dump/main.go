// Command dump prints the same snapshot the desktop app shows. It exists for
// checking the numbers from a terminal.
//
//	go run ./cmd/dump          one line per session and agent
//	go run ./cmd/dump -json    the full snapshot as JSON
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"monitor-agent/internal/monitor"
)

func main() {
	asJSON := flag.Bool("json", false, "print the full snapshot as JSON")
	flag.Parse()

	start := time.Now()
	snap := monitor.New(monitor.Options{}).Snapshot()
	took := time.Since(start)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	c := snap.Counts
	fmt.Printf("%d project, %d session (%d busy), %d agent (%d running, %d quiet)  [scan %s]\n",
		c.Projects, c.Sessions, c.BusySessions, c.Agents, c.RunningAgents, c.QuietAgents, took.Round(time.Millisecond))
	fmt.Printf("ALL   %s\n", snap.All)
	fmt.Printf("TODAY %s\n", snap.TodaySpend)
	for _, m := range snap.Models {
		fmt.Printf("  model %-12s priced=%-5v verified=%-5v %s\n", m.Label, m.Priced, m.Verified, m.All)
	}
	for _, p := range snap.Projects {
		fmt.Printf("\n%s  (%s)\n", p.Name, p.All)
		for _, s := range p.Sessions {
			recorded := "-"
			if s.RecordedCostUSD >= 0 {
				recorded = fmt.Sprintf("$%.4f", s.RecordedCostUSD)
			}
			fmt.Printf("  [%s] %q  %s  ctx=%d  own=(%s)  all=(%s)  claude-recorded=%s\n",
				s.Status, title(s), s.ModelLabel, s.ContextTokens, s.Own, s.All, recorded)
			for _, a := range s.Agents {
				fmt.Printf("    %-8s %-36.36s %-24.24s ctx=%-7d tools=%-3d %7s  %s\n",
					a.Status, a.Description, a.Type, a.ContextTokens, a.ToolUses,
					(time.Duration(a.DurationMs) * time.Millisecond).Truncate(time.Second), a.All)
			}
		}
	}
	for _, w := range snap.Warnings {
		fmt.Println("warning:", w)
	}
}

func title(s monitor.Session) string {
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}
