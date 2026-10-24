// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"errors"
	"time"
)

func (m *Module) notifyCaptureScheduler() {
	if m.captureWake == nil {
		return
	}
	select {
	case m.captureWake <- struct{}{}:
	default:
	}
}

func (m *Module) autoCaptureScheduler() {
	defer m.wg.Done()
	for {
		timer := time.NewTimer(m.autoCaptureInterval(m.closeCtx))
		select {
		case <-m.closeCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-m.captureWake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
		m.captureLocalSources(m.closeCtx)
	}
}

func (m *Module) autoCaptureInterval(ctx context.Context) time.Duration {
	cfg, err := m.loadTaskConfig(ctx)
	if err != nil || cfg.PollSeconds < 2 {
		return 15 * time.Second
	}
	return time.Duration(cfg.PollSeconds) * time.Second
}

// captureLocalSources reuses the same normalizer, revocation fence, coverage and
// deduplication path as an explicit scan. Remote host-history trees are imported by
// their SSH actor and must never be enumerated as local files.
func (m *Module) captureLocalSources(ctx context.Context) {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	sources, err := m.listLocalScanSources(ctx)
	if err != nil {
		return
	}

	result := ScanResult{MaxFiles: maxScanFiles}
	for _, source := range sources {
		if ctx.Err() != nil {
			return
		}
		if err := m.scanSource(ctx, "", source, &result); err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, errSourceRevoked) {
				continue
			}
			result.Errors++
			m.setSourceError(source.ID, err.Error())
			m.updateLastScan(source.ID, ScanResult{Errors: 1, MaxFiles: maxScanFiles}, "failed")
		}
	}
	if result.Changed > 0 {
		m.notifyTaskScheduler()
	}
}

func (m *Module) notifyTaskScheduler() {
	if m.taskWake == nil {
		return
	}
	select {
	case m.taskWake <- struct{}{}:
	default:
	}
}
