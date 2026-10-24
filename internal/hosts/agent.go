package hosts

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

const (
	HostAgentOwnerFile    = "agent.owner"
	HostAgentReadyFile    = "agent.ready"
	HostAgentTokenFile    = "access.token"
	NativeMagpieOwnerFile = "magpie.owner"
)

var hostAgentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// HostAgentOwner binds an installation to one canonical host and login uid.
// It is an ownership marker, not host identity authority.
type HostAgentOwner struct {
	HostID     string `json:"host_id"`
	UID        int    `json:"uid"`
	Hash       string `json:"hash"`
	Nonce      string `json:"nonce"`
	Executable string `json:"executable"`
}

// HostAgentReady is a non-secret loopback listener receipt. A ready receipt
// is valid only when its owner, uid, nonce, and binary hash all match.
type HostAgentReady struct {
	HostID    string `json:"host_id"`
	UID       int    `json:"uid"`
	Version   string `json:"version"`
	Hash      string `json:"hash"`
	Nonce     string `json:"nonce"`
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	StartedAt string `json:"started_at"`
}

// HostAgentStateDir returns the private per-host helper path below a selected
// XDG_STATE_HOME or the account's HOME/.local/state fallback.
func HostAgentStateDir(stateRoot, hostID string) (string, error) {
	if !path.IsAbs(stateRoot) || path.Clean(stateRoot) != stateRoot || strings.ContainsAny(stateRoot, "\x00\r\n") {
		return "", errors.New("la carpeta de estado del host no es válida")
	}
	if hostID == "" || hostID == "local" || !hostAgentIDPattern.MatchString(hostID) {
		return "", errors.New("ID de host-agent inválido")
	}
	return path.Join(stateRoot, "agent-workbench-next", "host-agent", hostID), nil
}
