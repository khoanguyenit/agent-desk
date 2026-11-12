//go:build !production

package main

import (
	"os"
	"path/filepath"

	"monitor-agent/internal/monitor"
)

// No policy in development: the Vite dev server needs inline styles and a
// websocket for hot reload.
const contentSecurityPolicy = ""

// realDataEnv must be set to "1" to let a development build read the real
// Claude Code directory and start sessions.
const realDataEnv = "MONITOR_AGENT_DEV_REAL_DATA"

// monitorOptions keeps `wails dev` harmless unless asked otherwise. The Wails
// dev server accepts websocket connections from any web page open in a
// browser on this machine, and such a page can call every bound method. In
// the built app there is no such server.
//
// So a development build, by default, reads no real transcripts, and is
// locked: it keeps no project list and refuses to start a session. A page
// that got in could otherwise start an agent in one of the user's projects
// and answer its permission requests itself. CLAUDE_CONFIG_DIR points the
// monitor at sample data and leaves the lock on; only realDataEnv lifts it.
func monitorOptions() (opts monitor.Options, notice string, locked bool) {
	if os.Getenv(realDataEnv) == "1" {
		return monitor.Options{}, "", false
	}
	const lock = "Bản dev đang khoá việc mở session và danh sách project: máy chủ dev của Wails cho mọi trang web đang mở trong trình duyệt gọi được vào app. " +
		"Đặt " + realDataEnv + "=1 nếu bạn chấp nhận rủi ro đó."
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return monitor.Options{}, lock, true
	}
	empty := filepath.Join(os.TempDir(), "monitor-agent-dev-no-data")
	return monitor.Options{ConfigDir: empty},
		"Bản dev không đọc dữ liệu thật. Đặt CLAUDE_CONFIG_DIR tới một thư mục mẫu để xem bảng theo dõi. " + lock, true
}
