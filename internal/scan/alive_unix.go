//go:build !windows

package scan

import (
	"math"
	"syscall"
)

// processAlive reports whether pid is running. The start marker is not
// compared here: its format on macOS and Linux has not been verified, so a
// reused pid cannot be told apart on these systems.
func processAlive(pid int, _ string) bool {
	// The upper bound matters: a larger value would wrap to a negative pid,
	// and signalling a negative pid addresses a whole process group.
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	// Signal 0 only checks. A permission error means the pid exists but
	// belongs to another user, so it is not this user's Claude Code session.
	return syscall.Kill(pid, 0) == nil
}
