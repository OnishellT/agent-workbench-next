package hosts

import (
	"context"
	"errors"
)

type hostService struct {
	store *Store
	ssh   *SSHManager
}

var _ Service = (*hostService)(nil)

func (s *hostService) List(ctx context.Context) ([]Host, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.store.Hosts()
	if err != nil {
		return nil, err
	}
	out := make([]Host, 0, len(rows))
	for _, row := range rows {
		out = append(out, projectHost(row))
	}
	return out, ctx.Err()
}

func (s *hostService) Get(ctx context.Context, id string) (Host, error) {
	if err := ctx.Err(); err != nil {
		return Host{}, err
	}
	row, err := s.store.Host(id)
	if err != nil {
		return Host{}, err
	}
	return projectHost(row), ctx.Err()
}

func (s *hostService) Identity(ctx context.Context, id string) (Identity, error) {
	row, err := s.Get(ctx, id)
	if err != nil {
		return Identity{}, err
	}
	if !row.Enabled {
		return Identity{}, ErrPaused
	}
	if !sshFingerprintPattern.MatchString(row.Fingerprint) {
		return Identity{}, ErrUntrusted
	}
	return row.Identity()
}

func (s *hostService) SSHOperation(ctx context.Context, id string, operation Operation) error {
	if s.ssh == nil {
		return errors.New("gestor SSH no disponible")
	}
	return s.ssh.SSHOperation(ctx, id, operation)
}

func projectHost(row Host) Host {
	return Host{ID: row.ID, Name: row.Name, Address: row.Address, User: row.User, Port: row.Port, Auth: row.Auth, Fingerprint: row.Fingerprint, ObservedFingerprint: row.ObservedFingerprint, Revision: row.Revision, Enabled: row.Enabled, Status: row.Status, Error: row.Error}
}
