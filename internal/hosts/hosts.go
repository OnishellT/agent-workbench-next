package hosts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	ErrPaused          = errors.New("el host está pausado")
	ErrUntrusted       = errors.New("la identidad SSH del host no está confirmada")
	ErrIdentityChanged = errors.New("la identidad SSH del host cambió")
	ErrHostExists      = errors.New("ya existe un host SSH con ese ID")
)

// Host is the persisted, secret-free SSH host record shared by the shell,
// Work, Terminal, Models, and conversation-history readers.
type Host struct {
	Revision            int64  `json:"revision"`
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Address             string `json:"address"`
	Hostname            string `json:"hostname"`
	IP                  string `json:"ip"`
	User                string `json:"user"`
	Port                int    `json:"port"`
	Auth                string `json:"auth"`
	KeyPath             string `json:"key_path,omitempty"`
	Enabled             bool   `json:"enabled"`
	PasswordSet         bool   `json:"password_set"`
	Fingerprint         string `json:"fingerprint,omitempty"`
	ObservedFingerprint string `json:"observed_fingerprint,omitempty"`
	Status              string `json:"status"`
	Error               string `json:"error,omitempty"`
	Home                string `json:"home,omitempty"`
	LastAttempt         string `json:"last_attempt,omitempty"`
	LastSync            string `json:"last_sync,omitempty"`
	Files               int    `json:"files"`
	SyncedFiles         int    `json:"synced_files"`
}

type HostInput struct {
	Host
	Password string `json:"password,omitempty"`
	// CreateOnly makes Save reject an existing id instead of replacing it.
	CreateOnly bool `json:"-"`
}

type Source struct {
	HostID      string `json:"host_id,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
	LastScan    string `json:"last_scan"`
	Files       int    `json:"files"`
	Error       string `json:"error,omitempty"`
	Description string `json:"description"`
}

var (
	hostIDPattern         = regexp.MustCompile("^[a-zA-Z0-9_-]{1,80}$")
	hostUserPattern       = regexp.MustCompile("^[a-zA-Z0-9_.-]{1,128}$")
	hostAddressPattern    = regexp.MustCompile("^[a-zA-Z0-9.:-]{1,253}$")
	hostNamePattern       = regexp.MustCompile("^[a-zA-Z0-9_.:-]{1,253}$")
	sshFingerprintPattern = regexp.MustCompile("^SHA256:[A-Za-z0-9+/]{43}$")
)

func (h *Host) Validate() error {
	if h.ID == "" {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		h.ID = fmt.Sprintf("ssh-%x", b)
	}
	if user, address, ok := strings.Cut(h.Address, "@"); ok {
		if h.User != "" && h.User != user {
			return fmt.Errorf("el usuario no coincide con la dirección")
		}
		h.User, h.Address = user, address
	}
	h.Address = strings.Trim(strings.TrimSpace(h.Address), "[]")
	if !hostIDPattern.MatchString(h.ID) || h.ID == "local" {
		return fmt.Errorf("ID de host inválido")
	}
	if !hostAddressPattern.MatchString(h.Address) || !validHostAddress(h.Address) || !hostUserPattern.MatchString(h.User) {
		return fmt.Errorf("indica una IP o nombre de host y un usuario SSH válidos")
	}
	if h.Port == 0 {
		h.Port = 22
	}
	if h.Port < 1 || h.Port > 65535 {
		return fmt.Errorf("puerto fuera de rango")
	}
	if h.Name == "" {
		h.Name = h.User + "@" + h.Address
	}
	if len(h.Name) > 120 || strings.ContainsAny(h.Name, "\x00\r\n") {
		return fmt.Errorf("nombre demasiado largo")
	}
	if h.Auth == "" {
		h.Auth = "password"
	}
	switch h.Auth {
	case "password", "key", "agent":
	default:
		return fmt.Errorf("autenticación inválida")
	}
	if h.Auth == "key" && (!filepath.IsAbs(h.KeyPath) || len(h.KeyPath) > 4096 || strings.ContainsRune(h.KeyPath, 0)) {
		return fmt.Errorf("indica la ruta absoluta de la clave privada local")
	}
	return nil
}

func validHostAddress(address string) bool {
	if net.ParseIP(address) != nil {
		return true
	}
	if strings.Contains(address, ":") || len(address) > 253 {
		return false
	}
	name := strings.TrimSuffix(address, ".")
	if name == "" {
		return false
	}
	numeric := true
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == 45 || label[len(label)-1] == 45 {
			return false
		}
		for _, char := range label {
			if char < 48 || char > 57 {
				numeric = false
			}
			if !(char >= 97 && char <= 122 || char >= 65 && char <= 90 || char >= 48 && char <= 57 || char == 45) {
				return false
			}
		}
	}
	return !numeric
}

// Identity binds an SSH operation to the exact host configuration the operator
// trusted. Digest follows the legacy host-history tuple and is not authority by itself.
type Identity struct {
	HostID      string
	Revision    int64
	Address     string
	User        string
	Port        int
	Fingerprint string
	Digest      string
}

// Identity returns a stable SHA-256 receipt for a confirmed host tuple.
func (h Host) Identity() (Identity, error) {
	if h.ID == "" || h.ID == "local" || h.Address == "" || h.User == "" || h.Port < 1 || h.Port > 65535 || h.Revision < 1 || h.Fingerprint == "" {
		return Identity{}, ErrUntrusted
	}
	if h.ObservedFingerprint != "" {
		return Identity{}, ErrIdentityChanged
	}
	encoded, err := json.Marshal([]any{h.ID, h.Address, h.Port, h.User, h.Fingerprint, h.Revision})
	if err != nil {
		return Identity{}, err
	}
	sum := sha256.Sum256(encoded)
	return Identity{HostID: h.ID, Revision: h.Revision, Address: h.Address, User: h.User, Port: h.Port, Fingerprint: h.Fingerprint, Digest: hex.EncodeToString(sum[:])}, nil
}

type Operation func(context.Context, *ssh.Client, Identity) error

type Registry interface {
	List(context.Context) ([]Host, error)
	Get(context.Context, string) (Host, error)
}

// Service owns canonical persisted host identity and performs each SSH operation
// only after revalidating the operator-confirmed fingerprint.
type Service interface {
	Registry
	Identity(context.Context, string) (Identity, error)
	SSHOperation(context.Context, string, Operation) error
}

func parserFamily(kind string) string {
	if kind == "pi" || kind == "omp" {
		return "pi"
	}
	return kind
}
