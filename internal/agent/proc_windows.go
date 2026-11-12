//go:build windows

package agent

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNoWindow keeps a console program started by a windowed program from
// opening a console window of its own.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// group holds a session's process together with everything that process
// starts: the commands it runs, its MCP servers, programs left in the
// background. It is a Windows job object set to end its processes when the
// last handle to it is closed. This program holds the only handle, so the
// whole tree goes when the session is closed and also when this program dies
// without the chance to clean up; nothing is left running unseen.
type group struct {
	mu  sync.Mutex
	job windows.Handle
	p   *os.Process
}

func newGroup() *group {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &group{}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return &group{}
	}
	return &group{job: job}
}

// add puts a started process in the group. A process it has already started
// by then would stay outside; the call is made at once after the start, when
// Claude Code has not run anything yet.
func (g *group) add(p *os.Process) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.p = p
	if g.job == 0 {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.AssignProcessToJobObject(g.job, h)
}

// kill ends every process of the group now.
func (g *group) kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = windows.TerminateJobObject(g.job, 1)
	}
	if g.p != nil {
		_ = g.p.Kill()
	}
}

// release gives the group up once its first process has exited. Whatever
// that process left running is ended with it.
func (g *group) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}
