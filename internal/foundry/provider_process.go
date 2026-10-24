// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

func runProviderProcess(ctx context.Context, path string, args []string, workDir, stdin string, env []string) (providerProcessResult, error) {
	if path == "" {
		return providerProcessResult{}, errors.New("ejecutable de proveedor vacío")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = workDir
	cmd.Env = append([]string(nil), env...)
	cmd.Stdin = strings.NewReader(stdin)
	stdout := &boundedBuffer{limit: maxProviderOutput}
	stderr := &boundedBuffer{limit: maxProviderOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	prepareProviderProcess(cmd)
	started := time.Now()
	err := cmd.Run()
	result := providerProcessResult{stdout: stdout.data.Bytes(), stderr: stderr.data.Bytes(), seconds: time.Since(started).Seconds(), overflow: stdout.overflow || stderr.overflow}
	if err != nil {
		return result, providerExitError(ctx, result.stderr, err)
	}
	return result, nil
}
