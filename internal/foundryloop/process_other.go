//go:build !unix

package foundryloop

import (
	"os/exec"
	"syscall"
	"time"
)

func processAttr() *syscall.SysProcAttr { return nil }

func killGroup(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
