//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"syscall"
)

// The session's process leads a process group of its own, so that it and
// everything it started can be ended together.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// group holds a session's process and what it starts. Unlike its Windows
// counterpart it cannot end them when this program itself dies unexpectedly.
type group struct {
	p *os.Process
}

func newGroup() *group { return &group{} }

func (g *group) add(p *os.Process) { g.p = p }

func (g *group) kill() {
	if g.p == nil {
		return
	}
	// A negative pid addresses the whole process group.
	if err := syscall.Kill(-g.p.Pid, syscall.SIGKILL); err != nil {
		_ = g.p.Kill()
	}
}

func (g *group) release() {
	if g.p != nil {
		_ = syscall.Kill(-g.p.Pid, syscall.SIGKILL)
	}
}
