// SPDX-License-Identifier: AGPL-3.0-only
//go:build !unix

package foundry

import (
	"os/exec"
	"time"
)

func prepareProviderProcess(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
