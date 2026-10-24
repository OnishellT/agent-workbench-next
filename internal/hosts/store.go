package hosts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB  *sql.DB
	Dir string
}

// OpenStore reuses Atlas's historical database path and host/source tables so
// retirement never drops or relocates operator-owned host identity or secrets.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS hosts(id TEXT PRIMARY KEY,payload TEXT NOT NULL,secret BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS sources(id TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS host_history(host_id TEXT PRIMARY KEY,payload TEXT NOT NULL);`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{DB: db, Dir: filepath.Dir(path)}, nil
}

func (s *Store) Host(id string) (Host, error) {
	var host Host
	var raw string
	err := s.DB.QueryRow("SELECT payload FROM hosts WHERE id=?", id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &host)
	}
	return host, err
}

func (s *Store) Hosts() ([]Host, error) {
	rows, err := s.DB.Query("SELECT payload FROM hosts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := []Host{}
	for rows.Next() {
		var raw string
		var host Host
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &host); err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}

func (s *Store) putHost(host Host, secret []byte) error {
	raw, err := json.Marshal(host)
	if err != nil {
		return err
	}
	if secret == nil {
		secret = []byte{}
	}
	_, err = s.DB.Exec("INSERT INTO hosts VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,secret=excluded.secret", host.ID, string(raw), secret)
	return err
}

func (s *Store) hostSecret(id string) ([]byte, error) {
	var secret []byte
	err := s.DB.QueryRow("SELECT secret FROM hosts WHERE id=?", id).Scan(&secret)
	return secret, err
}

// hostState updates only observations for the revision that initiated them.
func (s *Store) hostState(observed Host) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow("SELECT payload FROM hosts WHERE id=?", observed.ID).Scan(&raw); err != nil {
		return err
	}
	var current Host
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if current.Revision != observed.Revision {
		return nil
	}
	current.Status, current.Error = observed.Status, observed.Error
	current.Home = observed.Home
	current.Hostname, current.IP = observed.Hostname, observed.IP
	current.Fingerprint = observed.Fingerprint
	current.ObservedFingerprint = observed.ObservedFingerprint
	current.LastAttempt = observed.LastAttempt
	current.LastSync = observed.LastSync
	current.Files = observed.Files
	current.SyncedFiles = observed.SyncedFiles
	if !current.Enabled {
		current.Status = "paused"
	}
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE hosts SET payload=? WHERE id=?", string(b), current.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Source(id string) (Source, error) {
	var value Source
	var raw string
	err := s.DB.QueryRow("SELECT payload FROM sources WHERE id=?", id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &value)
	}
	return value, err
}

func (s *Store) Sources() ([]Source, error) {
	rows, err := s.DB.Query("SELECT payload FROM sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var raw string
		var source Source
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &source); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, rows.Err()
}

func (s *Store) PutSource(source Source) error {
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO sources VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", source.ID, string(raw))
	return err
}

func (s *Store) sourceState(observed Source) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow("SELECT payload FROM sources WHERE id=?", observed.ID).Scan(&raw); err != nil {
		return err
	}
	var current Source
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return err
	}
	if current.Path != observed.Path || current.HostID != observed.HostID || current.Enabled != observed.Enabled {
		return nil
	}
	current.Status, current.Error = observed.Status, observed.Error
	current.LastScan, current.Files = observed.LastScan, observed.Files
	b, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE sources SET payload=? WHERE id=?", string(b), current.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func secretCipher(dir string) (cipher.AEAD, error) {
	path := filepath.Join(dir, "ssh-secrets.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(createErr) {
			key, err = os.ReadFile(path)
		} else if createErr != nil {
			return nil, createErr
		} else {
			_, err = f.Write(key)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("clave local de credenciales inválida")
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealPassword(aead cipher.AEAD, id, password string) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(password), []byte(id)), nil
}

func openPassword(aead cipher.AEAD, id string, secret []byte) (string, error) {
	if len(secret) < aead.NonceSize() {
		return "", fmt.Errorf("falta una contraseña guardada")
	}
	plain, err := aead.Open(nil, secret[:aead.NonceSize()], secret[aead.NonceSize():], []byte(id))
	if err != nil {
		return "", fmt.Errorf("no se pudieron descifrar las credenciales del host")
	}
	return string(plain), nil
}

func (s *Store) source(id string) (Source, error) { return s.Source(id) }
