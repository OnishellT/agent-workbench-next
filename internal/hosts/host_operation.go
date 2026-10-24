package hosts

import (
	"context"
	"errors"
)

type activeSSHOperation struct {
	cancel   context.CancelFunc
	revision int64
}

type hostKeyError struct {
	changed bool
}

func (e *hostKeyError) Error() string {
	if e.changed {
		return "la huella SSH cambió; conexión bloqueada hasta confirmación explícita"
	}
	return "huella SSH desconocida; confirma la identidad antes de operar"
}

func (m *SSHManager) cancelOperationsLocked(id string) {
	for operation := range m.operations[id] {
		operation.cancel()
	}
}

// TrustFingerprint only accepts the key observed during a blocked handshake and
// the host revision the operator reviewed; it never performs trust-on-first-use.
func (m *SSHManager) TrustFingerprint(id string, revision int64, fingerprint string) (Host, error) {
	if len(fingerprint) != 50 || !sshFingerprintPattern.MatchString(fingerprint) {
		return Host{}, errors.New("huella SSH inválida")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Host{}, errors.New("servicio SSH detenido")
	}
	h, err := m.Store.Host(id)
	if err != nil {
		return Host{}, err
	}
	if h.Revision != revision {
		return Host{}, ErrIdentityChanged
	}
	if h.ObservedFingerprint != fingerprint {
		return Host{}, errors.New("la huella no coincide con la clave observada; vuelve a comprobar el host")
	}
	secret, err := m.Store.hostSecret(id)
	if err != nil {
		return Host{}, err
	}
	h.Fingerprint = fingerprint
	h.ObservedFingerprint = ""
	h.Revision++
	h.Error = ""
	h.Status = "pending"
	if !h.Enabled {
		h.Status = "paused"
	}
	if sync := m.running[id]; sync != nil {
		sync.cancel()
	}
	m.cancelOperationsLocked(id)
	if err = m.Store.putHost(h, secret); err != nil {
		return Host{}, err
	}
	m.cancelConversationsLocked(id)
	return h, nil
}

// SSHOperation opens one short-lived, pinned connection for the callback. Save,
// trust changes, pause, and Close cancel its context and close the SSH socket.
func (m *SSHManager) SSHOperation(ctx context.Context, id string, operation Operation) error {
	if operation == nil {
		return errors.New("operación SSH obligatoria")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("servicio SSH detenido")
	}
	h, err := m.Store.Host(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if !h.Enabled {
		m.mu.Unlock()
		return ErrPaused
	}
	if h.Fingerprint == "" || !sshFingerprintPattern.MatchString(h.Fingerprint) {
		m.mu.Unlock()
		return ErrUntrusted
	}
	identity, err := projectHost(h).Identity()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	opCtx, cancel := context.WithCancel(ctx)
	active := &activeSSHOperation{cancel: cancel, revision: h.Revision}
	if m.operations[id] == nil {
		m.operations[id] = make(map[*activeSSHOperation]struct{})
	}
	m.operations[id][active] = struct{}{}
	m.wg.Add(1)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.operations[id], active)
		if len(m.operations[id]) == 0 {
			delete(m.operations, id)
		}
		m.mu.Unlock()
		m.wg.Done()
	}()

	client, err := m.connect(opCtx, &h)
	if err != nil {
		if h.ObservedFingerprint != "" {
			h.Status = "error"
			h.Error = truncate(err.Error(), 300)
			if persistErr := m.Store.hostState(h); persistErr != nil {
				return errors.Join(err, persistErr)
			}
		}
		return err
	}
	stop := context.AfterFunc(opCtx, func() { _ = client.Close() })
	defer stop()
	defer client.Close()
	defer startSSHKeepalive(opCtx, client)()
	if err := opCtx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("servicio SSH detenido")
	}
	current, err := m.Store.Host(id)
	if err == nil {
		switch {
		case !current.Enabled:
			err = ErrPaused
		case current.ObservedFingerprint != "":
			err = ErrIdentityChanged
		case current.Revision != active.revision || current.Address != identity.Address || current.User != identity.User || current.Port != identity.Port || current.Fingerprint != identity.Fingerprint:
			err = ErrIdentityChanged
		}
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return operation(opCtx, client, identity)
}
