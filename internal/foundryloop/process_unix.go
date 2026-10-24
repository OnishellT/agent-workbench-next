//go:build unix

package foundryloop

import (
	"os/exec"
	"syscall"
	"time"
)

// processAttr gives Foundry its own process group: terminal signals aimed at Workbench do
// not reach it (Close stops it explicitly) and the agents it launches die with the group.
func processAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
