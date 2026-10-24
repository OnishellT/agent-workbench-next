package foundry

import (
	"context"
	"errors"
	"time"
)

func (m *Module) memuProofAuthorized(ctx context.Context, settings memuStoredState) bool {
	base, err := m.memuBase()
	return err == nil && settings.Published && m.memuManifestAuthorized(ctx, base)
}

// MemuEvidenceAuthorized is shared by the artifact review/adoption boundary.
// Rollback uses its own receipt and destination hash, so revocation cannot block undo.
func (m *Module) MemuEvidenceAuthorized() error {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return errors.New("el servicio está cerrándose")
	}
	settings, _, apiErr := m.loadMemuSettings()
	if apiErr != nil || !m.memuProofAuthorized(m.closeCtx, settings) {
		return errors.New("las propuestas no tienen evidencia vigente autorizada")
	}
	return nil
}

func (m *Module) watchMemuEvidence(ctx context.Context, cancel context.CancelFunc, base string) func() {
	watchCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				if !m.memuManifestAuthorized(watchCtx, base) {
					cancel()
					return
				}
			}
		}
	}()
	return func() { stop(); <-done }
}
