// SPDX-License-Identifier: AGPL-3.0-only
//go:build unix

package foundry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProviderCancellationKillsProcessGroup(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runProviderProcess(ctx, sh, []string{"-c", "sleep 30 & echo $! > \"$1.tmp\"; mv \"$1.tmp\" \"$1\"; wait", "sh", pidFile}, dir, "", []string{"PATH=" + os.Getenv("PATH")})
		done <- err
	}()
	var pid int
	waitFor(t, 3*time.Second, func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil && pid > 0
	})
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider process error=%v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled provider process group did not exit")
	}
	waitFor(t, 3*time.Second, func() bool { return !processAlive(pid) })
}

// processAlive reports a live (non-zombie) process; a zombie awaiting an init that does
// not reap is already dead for this purpose.
func processAlive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if end := bytes.LastIndexByte(raw, ')'); end >= 0 && end+2 < len(raw) && raw[end+2] == 'Z' {
			return false
		}
	}
	return true
}
