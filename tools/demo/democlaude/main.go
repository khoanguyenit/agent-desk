// A stand-in for the claude program that plays one scripted session, for
// recording a demo of the app without spending tokens or showing real work.
// It speaks the JSON lines of Claude Code 2.1.322 and leaves a registry entry
// and a transcript in CLAUDE_CONFIG_DIR, as the real program does.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	sessionID string
	cwd       string
	configDir = os.Getenv("CLAUDE_CONFIG_DIR")
	turn      int
)

func out(v any) {
	raw, _ := json.Marshal(v)
	fmt.Println(string(raw))
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func procStart() string {
	var creation, exit, kernel, user syscall.Filetime
	h, _ := syscall.GetCurrentProcess()
	if syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user) != nil {
		return ""
	}
	return fmt.Sprint(uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime))
}

func registry(status string) {
	if configDir == "" {
		return
	}
	dir := filepath.Join(configDir, "sessions")
	_ = os.MkdirAll(dir, 0o700)
	raw, _ := json.Marshal(map[string]any{
		"pid": os.Getpid(), "sessionId": sessionID, "cwd": cwd, "status": status, "name": "order-service-1",
		"procStart": procStart(), "entrypoint": "sdk-cli", "version": "2.1.322",
		"startedAt": time.Now().UnixMilli(), "updatedAt": time.Now().UnixMilli(),
	})
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())), raw, 0o600)
}

func transcript(lines ...any) {
	if configDir == "" {
		return
	}
	dir := filepath.Join(configDir, "projects", slug(cwd))
	_ = os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, sessionID+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, l := range lines {
		raw, _ := json.Marshal(l)
		f.Write(append(raw, '\n'))
	}
}

// spent writes one response to the transcript, so the board has something to
// count: tokens, cost, and the session's latest activity.
func spent(block map[string]any, output int) {
	turn++
	// The first response pays for loading the system prompt; the later ones
	// read it back from the cache. The total comes to what the session
	// reports at its end, so the board and the panel show the same cost.
	written, read := 2400, 31000+turn*2600
	if turn == 1 {
		written, read = 30000, 0
	}
	transcript(map[string]any{
		"type": "assistant", "isSidechain": false, "sessionId": sessionID, "cwd": cwd,
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message": map[string]any{
			"id": fmt.Sprintf("msg_demo_%d", turn), "model": "claude-sonnet-5-5", "stop_reason": "tool_use",
			"content": []any{block},
			"usage": map[string]any{
				"input_tokens": 9, "output_tokens": output, "cache_creation_input_tokens": written, "cache_read_input_tokens": read,
				"cache_creation": map[string]any{"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": written},
			},
		},
	})
}

func assistant(blocks ...any) any {
	return map[string]any{"type": "assistant", "parent_tool_use_id": nil, "message": map[string]any{"content": blocks}}
}

func event(e map[string]any) any {
	return map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": e}
}

func toolResult(id, text string, failed bool) any {
	return map[string]any{"type": "user", "parent_tool_use_id": nil, "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": failed, "content": text},
	}}}
}

func thinking(d time.Duration) {
	out(event(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking"}}))
	time.Sleep(d)
}

// say streams text a few characters at a time and then gives its final form.
func say(text string) {
	out(event(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}))
	runes := []rune(text)
	for i := 0; i < len(runes); i += 6 {
		end := min(i+6, len(runes))
		out(event(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": string(runes[i:end])}}))
		time.Sleep(40 * time.Millisecond)
	}
	out(assistant(map[string]any{"type": "text", "text": text}))
	spent(map[string]any{"type": "text", "text": text}, 220)
}

const (
	// Kept narrow, so that no line wraps in the side panel of the clip.
	oldCode = "\tif err := req.Validate(); err != nil {\n" +
		"\t\treturn nil, err\n" +
		"\t}\n" +
		"\treturn h.store.Insert(ctx, req.ToOrder())"
	newCode = "\tif err := req.Validate(); err != nil {\n" +
		"\t\treturn nil, err\n" +
		"\t}\n" +
		"\t// Sent again with the same key: no second order.\n" +
		"\tif prev, ok := h.store.ByKey(ctx, req.Key); ok {\n" +
		"\t\treturn prev, nil\n" +
		"\t}\n" +
		"\treturn h.store.Insert(ctx, req.ToOrder())"
)

func main() {
	mode := "default"
	for i, a := range os.Args {
		if i+1 >= len(os.Args) {
			break
		}
		switch a {
		case "--session-id":
			sessionID = os.Args[i+1]
		case "--permission-mode":
			if mode = os.Args[i+1]; mode == "manual" {
				mode = "default"
			}
		}
	}
	cwd, _ = os.Getwd()
	registry("busy")
	out(map[string]any{"type": "system", "subtype": "init", "session_id": sessionID, "model": "claude-sonnet-5-5", "permissionMode": mode})

	handler := filepath.Join(cwd, "internal", "order", "handler.go")
	step := 0 // which permission request is waiting: 1 the edit, 2 the test run
	ask := func(id, tool string, input map[string]any) {
		out(assistant(map[string]any{"type": "tool_use", "id": id, "name": tool, "input": input}))
		spent(map[string]any{"type": "tool_use", "id": id, "name": tool, "input": input}, 140)
		out(map[string]any{"type": "control_request", "request_id": "perm-" + id, "request": map[string]any{
			"subtype": "can_use_tool", "tool_name": tool, "input": input, "tool_use_id": id,
		}})
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for in.Scan() {
		var l struct {
			Type    string `json:"type"`
			Request struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
			RequestID string `json:"request_id"`
			Response  struct {
				Response struct {
					Behavior string `json:"behavior"`
				} `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(in.Bytes(), &l) != nil {
			continue
		}
		switch {
		case l.Type == "user":
			registry("busy")
			transcript(map[string]any{"type": "ai-title", "aiTitle": "Chống tạo lệnh trùng khi client gửi lại", "sessionId": sessionID})
			thinking(1300 * time.Millisecond)
			say("Tôi xem handler tạo lệnh trước.")
			read := map[string]any{"file_path": handler}
			out(assistant(map[string]any{"type": "tool_use", "id": "t-read", "name": "Read", "input": read}))
			spent(map[string]any{"type": "tool_use", "id": "t-read", "name": "Read", "input": read}, 60)
			time.Sleep(900 * time.Millisecond)
			out(toolResult("t-read", "package order\n\n"+oldCode, false))
			thinking(900 * time.Millisecond)
			step = 1
			ask("t-edit", "Edit", map[string]any{"file_path": handler, "old_string": oldCode, "new_string": newCode})

		case l.Type == "control_response" && l.Response.Response.Behavior != "allow":
			out(toolResult(map[int]string{1: "t-edit", 2: "t-test"}[step], "The user declined this action.", true))
			say("Tôi dừng ở đây vì bạn đã từ chối.")
			registry("idle")
			out(map[string]any{"type": "result", "subtype": "success", "num_turns": 1, "total_cost_usd": 0.14})

		case l.Type == "control_response" && step == 1:
			out(toolResult("t-edit", "The file has been updated.", false))
			thinking(700 * time.Millisecond)
			step = 2
			ask("t-test", "Bash", map[string]any{"command": "go test ./internal/order/...", "description": "Chạy test của package order"})

		case l.Type == "control_response" && step == 2:
			// The tests take a moment.
			time.Sleep(1800 * time.Millisecond)
			out(toolResult("t-test", "ok  \torder-service/internal/order\t0.412s", false))
			thinking(600 * time.Millisecond)
			say("Đã thêm kiểm tra idempotency key vào `CreateOrder`: lệnh gửi lại với cùng key nhận về lệnh đã tạo lần đầu, không tạo lệnh mới.\n\nTest của package `order` qua hết.")
			registry("idle")
			out(map[string]any{"type": "result", "subtype": "success", "is_error": false, "num_turns": 4, "total_cost_usd": 0.18})

		case l.Type == "control_request" && l.Request.Subtype == "interrupt":
			out(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": l.RequestID, "response": map[string]any{"still_queued": []any{}}}})
			registry("idle")
			out(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "num_turns": 1, "total_cost_usd": 0.1})
		}
	}
	if configDir != "" {
		_ = os.Remove(filepath.Join(configDir, "sessions", fmt.Sprintf("%d.json", os.Getpid())))
	}
}
