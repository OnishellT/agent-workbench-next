package hosts

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Module owns the persisted SSH registry, pinned transport, helper routes, and
// remote conversation readers. The legacy directory keeps existing host data
// and encrypted credentials in place; tracker tables are left untouched.
type Module struct {
	Store       *Store
	SSH         *SSHManager
	server      *Server
	hostService *hostService
	mu          sync.Mutex
	closed      bool
}

func NewModule(stateDir string) (*Module, error) {
	if stateDir == "" {
		return nil, errors.New("stateDir es obligatorio")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(stateDir, "usage.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	sshManager, err := NewSSHManager(store)
	if err != nil {
		_ = store.DB.Close()
		return nil, err
	}
	service := &hostService{store: store, ssh: sshManager}
	server := &Server{Store: store, SSH: sshManager, hostService: service, DBPath: dbPath}
	return &Module{Store: store, SSH: sshManager, server: server, hostService: service}, nil
}

func (m *Module) Handler() http.Handler      { return m.server.Handler() }
func (m *Module) HostsHandler() http.Handler { return m.Handler() }
func (m *Module) HostsService() Service      { return m.hostService }

func (m *Module) SetHistoryImporter(importer HistoryImporter) {
	m.server.HistoryImporter = importer
}

func (m *Module) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()
	m.StopHistoryAutoSync()
	m.SSH.Close()
	return m.Store.DB.Close()
}
