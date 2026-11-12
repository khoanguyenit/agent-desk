// Command agentprobe drives one headless Claude Code process by hand and
// prints what crosses the wire. It exists to check, against the installed
// Claude Code version, the parts of the stream-json protocol that the app's
// chat feature depends on and that are not documented for third-party hosts:
// permission requests and interrupts.
//
//	go run ./cmd/agentprobe -claude <path to claude> -dir <folder> -decide deny "first prompt" "second prompt"
//
// Every run starts a real session and spends real tokens.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type line struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	Mode      string          `json:"permissionMode"`
	Caps      []string        `json:"capabilities"`
	Result    string          `json:"result"`
	IsError   bool            `json:"is_error"`
	Cost      float64         `json:"total_cost_usd"`
	Turns     int             `json:"num_turns"`
	Denials   json.RawMessage `json:"permission_denials"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func main() {
	claude := flag.String("claude", "", "path to the claude executable")
	dir := flag.String("dir", ".", "working directory for the session")
	model := flag.String("model", "sonnet", "model alias")
	mode := flag.String("mode", "", "permission mode (empty: the CLI's default for -p)")
	decide := flag.String("decide", "deny", "answer to permission requests: allow, deny, or none (do not answer)")
	promptTool := flag.Bool("prompt-tool-stdio", true, "pass --permission-prompt-tool stdio, as the SDKs do")
	initialize := flag.Bool("initialize", false, "send an initialize control request first, as the SDKs do")
	interruptAfter := flag.Duration("interrupt-after", 0, "send an interrupt control request this long after the first prompt")
	persist := flag.Bool("persist", false, "keep the session on disk (default: --no-session-persistence)")
	flag.Parse()
	prompts := flag.Args()
	if *claude == "" || len(prompts) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agentprobe -claude <exe> [flags] <prompt> [<prompt>...]")
		os.Exit(2)
	}

	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--model", *model}
	if *promptTool {
		args = append(args, "--permission-prompt-tool", "stdio")
	}
	if *mode != "" {
		args = append(args, "--permission-mode", *mode)
	}
	if !*persist {
		args = append(args, "--no-session-persistence")
	}
	cmd := exec.Command(*claude, args...)
	cmd.Dir = *dir
	// The SDKs clear this marker so the child does not think it is nested.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	cmd.Stderr = os.Stderr
	fmt.Println("$ claude", strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	start := time.Now()
	stamp := func() string { return fmt.Sprintf("%5.1fs", time.Since(start).Seconds()) }

	var mu sync.Mutex
	send := func(v any) {
		raw, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		fmt.Printf("%s  >> %s\n", stamp(), clip(string(raw), 300))
		if _, err := stdin.Write(append(raw, '\n')); err != nil {
			fmt.Println("   write failed:", err)
		}
	}
	user := func(text string) {
		send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}, "parent_tool_use_id": nil})
	}

	if *initialize {
		send(map[string]any{"type": "control_request", "request_id": "req_init", "request": map[string]any{"subtype": "initialize", "hooks": nil}})
	}
	user(prompts[0])
	next := 1
	if *interruptAfter > 0 {
		time.AfterFunc(*interruptAfter, func() {
			send(map[string]any{"type": "control_request", "request_id": "req_interrupt", "request": map[string]any{"subtype": "interrupt"}})
		})
	}

	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 {
			var l line
			if json.Unmarshal(raw, &l) != nil {
				fmt.Printf("%s  << (not JSON) %s\n", stamp(), clip(string(raw), 200))
			} else {
				switch l.Type {
				case "control_request":
					// Printed in full: this is the shape being verified.
					fmt.Printf("%s  << control_request %s\n", stamp(), strings.TrimSpace(string(raw)))
					var req struct {
						Subtype string          `json:"subtype"`
						Input   json.RawMessage `json:"input"`
					}
					_ = json.Unmarshal(l.Request, &req)
					if req.Subtype == "can_use_tool" {
						switch *decide {
						case "allow":
							send(map[string]any{"type": "control_response", "response": map[string]any{
								"subtype": "success", "request_id": l.RequestID,
								"response": map[string]any{"behavior": "allow", "updatedInput": req.Input}}})
						case "deny":
							send(map[string]any{"type": "control_response", "response": map[string]any{
								"subtype": "success", "request_id": l.RequestID,
								"response": map[string]any{"behavior": "deny", "message": "The user declined this action.", "interrupt": false}}})
						}
					}
				case "control_response":
					fmt.Printf("%s  << control_response %s\n", stamp(), clip(string(raw), 400))
				case "system":
					if l.Subtype == "init" {
						fmt.Printf("%s  << system/init session=%s model=%s mode=%s capabilities=%v\n", stamp(), l.SessionID, l.Model, l.Mode, l.Caps)
					} else {
						fmt.Printf("%s  << system/%s\n", stamp(), l.Subtype)
					}
				case "assistant", "user":
					var blocks []block
					_ = json.Unmarshal(l.Message.Content, &blocks)
					for _, b := range blocks {
						switch b.Type {
						case "text":
							fmt.Printf("%s  << %s text: %s\n", stamp(), l.Type, clip(b.Text, 160))
						case "tool_use":
							fmt.Printf("%s  << %s tool_use %s %s\n", stamp(), l.Type, b.Name, clip(string(b.Input), 160))
						case "tool_result":
							fmt.Printf("%s  << %s tool_result error=%v %s\n", stamp(), l.Type, b.IsError, clip(string(b.Content), 160))
						default:
							fmt.Printf("%s  << %s %s\n", stamp(), l.Type, b.Type)
						}
					}
				case "result":
					fmt.Printf("%s  << result/%s error=%v turns=%d cost=$%.4f denials=%s text=%s\n",
						stamp(), l.Subtype, l.IsError, l.Turns, l.Cost, clip(string(l.Denials), 200), clip(l.Result, 160))
					if next < len(prompts) {
						user(prompts[next])
						next++
					} else {
						fmt.Printf("%s  -- closing stdin\n", stamp())
						_ = stdin.Close()
					}
				default:
					fmt.Printf("%s  << %s/%s\n", stamp(), l.Type, l.Subtype)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				fmt.Println("read error:", err)
			}
			break
		}
	}
	err = cmd.Wait()
	fmt.Printf("%s  process exited: %v\n", stamp(), err)
}
