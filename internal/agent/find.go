package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ClaudeEnv names an environment variable that points at the claude
// executable, for machines where it cannot be found automatically.
const ClaudeEnv = "MONITOR_AGENT_CLAUDE"

func isProgram(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// FindClaude locates the Claude Code executable. It looks, in order, at the
// ClaudeEnv variable, the PATH, the standalone installer's location, and
// finally the private copy that the Claude Code extension for VS Code and its
// relatives carries. That last location is not documented, so it is only a
// fallback for machines that have the extension and nothing else.
func FindClaude() (string, error) {
	if p := strings.TrimSpace(os.Getenv(ClaudeEnv)); p != "" {
		if isProgram(p) {
			return p, nil
		}
		return "", fmt.Errorf("biến %s trỏ tới một file không tồn tại: %s", ClaudeEnv, p)
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("không xác định được thư mục người dùng để tìm Claude Code")
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	if p := filepath.Join(home, ".local", "bin", name); isProgram(p) {
		return p, nil
	}

	var newest string
	var newestAt time.Time
	for _, editor := range []string{".vscode", ".vscode-insiders", ".cursor", ".windsurf", ".vscode-oss"} {
		pattern := filepath.Join(home, editor, "extensions", "anthropic.claude-code-*", "resources", "native-binary", name)
		matches, _ := filepath.Glob(pattern)
		for _, p := range matches {
			st, err := os.Stat(p)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			if newest == "" || st.ModTime().After(newestAt) {
				newest, newestAt = p, st.ModTime()
			}
		}
	}
	if newest != "" {
		return newest, nil
	}
	return "", fmt.Errorf("không tìm thấy Claude Code trên máy này. Cài bản dòng lệnh của Claude Code, hoặc đặt biến môi trường %s trỏ tới file %s", ClaudeEnv, name)
}
