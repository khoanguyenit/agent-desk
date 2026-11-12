//go:build windows

package scan

import (
	"math"
	"strconv"

	"golang.org/x/sys/windows"
)

// processAlive reports whether pid is running and, when procStart is given,
// whether it is the same process that wrote the registry entry. On Windows
// Claude Code records the process creation time as a FILETIME, so a reused pid
// is detected exactly.
func processAlive(pid int, procStart string) bool {
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	// A user can always open their own processes with this access. A failure,
	// access denied included, therefore means the pid now belongs to a system
	// service or another user: not the Claude Code session that wrote the entry.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	// A process handle becomes signalled when the process exits, so a wait
	// that times out at once means it is still running.
	if ev, err := windows.WaitForSingleObject(h, 0); err != nil || ev != uint32(windows.WAIT_TIMEOUT) {
		return false
	}
	want, err := strconv.ParseUint(procStart, 10, 64)
	if err != nil {
		return true
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return true
	}
	got := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	return got == want
}
