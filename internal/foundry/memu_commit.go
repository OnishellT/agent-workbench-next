package foundry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type memuAdoptionRecord struct {
	ID           string `json:"id"`
	Project      string `json:"project"`
	Target       string `json:"target"`
	SourceSHA256 string `json:"source_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	RolledBack   bool   `json:"rolled_back"`
}
type memuIndexBackup struct {
	Name     string         `json:"name"`
	Previous map[string]any `json:"previous"`
}

func (m *Module) memuAdoption(id string) (memuAdoptionRecord, string, error) {
	if len(id) != 16 {
		return memuAdoptionRecord{}, "", errors.New("recibo inválido")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return memuAdoptionRecord{}, "", errors.New("recibo inválido")
	}
	base, err := m.memuBase()
	if err != nil {
		return memuAdoptionRecord{}, "", err
	}
	dir := filepath.Join(base, "adoptions")
	if err := ensurePrivateDir(dir); err != nil {
		return memuAdoptionRecord{}, "", err
	}
	raw, err := readMemuFile(filepath.Join(dir, id+".json"), 1<<20)
	if err != nil {
		return memuAdoptionRecord{}, "", err
	}
	var record memuAdoptionRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.ID != id {
		return record, dir, errors.New("recibo inválido")
	}
	allowed := false
	if m.projectRoots != nil {
		for _, root := range m.projectRoots() {
			if filepath.Clean(root) == record.Project {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return record, dir, errors.New("proyecto no registrado")
	}
	return record, dir, nil
}

func (m *Module) memuMemoryCommand(ctx context.Context, args []string, input any) ([]byte, error) {
	binary, apiErr := requireMemuBinary()
	if apiErr != nil {
		return nil, errors.New(apiErr.Message)
	}
	if apiErr := requireMemuConfiguration(); apiErr != nil {
		return nil, errors.New(apiErr.Message)
	}
	cli := filepath.Join(filepath.Dir(binary), "memu")
	info, err := os.Lstat(cli)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, errors.New("el CLI original memu no está disponible")
	}
	prompt := ""
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		prompt = string(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := runProviderProcess(ctx, cli, args, m.stateDir, prompt, memuChildEnvironment())
	if err != nil {
		return nil, errors.New(safeLearningError(err.Error(), 600))
	}
	return result.stdout, nil
}

func (m *Module) commitMemu(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"adoption_id"`
	}
	if apiErr := decodeBody(w, r, &input); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !m.memuMu.TryLock() {
		memuBusy(w)
		return
	}
	defer m.memuMu.Unlock()
	ctx, cancel := m.memuRequestContext(r.Context())
	defer cancel()
	record, dir, err := m.memuAdoption(input.ID)
	if err == nil && record.RolledBack {
		err = m.restoreMemuIndexLocked(ctx, input.ID)
	} else if err == nil {
		err = m.indexMemuAdoption(ctx, record, dir)
	}
	if err != nil {
		writeAPIError(w, &APIError{Status: 409, Code: "memu_index_failed", Message: "No se pudo actualizar la memoria de memU: " + safeLearningError(err.Error(), 700)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"adoption_id": input.ID, "indexed": !record.RolledBack, "memory_restored": record.RolledBack})
}

func (m *Module) indexMemuAdoption(ctx context.Context, record memuAdoptionRecord, dir string) error {
	if !strings.EqualFold(filepath.Ext(record.Target), ".md") {
		return errors.New("solo se indexan adopciones Markdown; los scripts y recursos se conservan como archivos")
	}
	if !m.memuEvidenceFileAuthorized(ctx, filepath.Join(dir, record.ID+".evidence")) {
		return errors.New("la evidencia original fue revocada o cambió")
	}
	root, err := filepath.EvalSymlinks(record.Project)
	if err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(record.Project, record.Target))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return errors.New("el destino escapa del proyecto")
	}
	current, err := readMemuFile(target, 1<<20)
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(current))
	if hex.EncodeToString(hash[:]) != record.AfterSHA256 {
		return errors.New("el destino cambió desde la adopción")
	}
	approved, err := readMemuFile(filepath.Join(dir, record.ID+".approved"), 1<<20)
	if err != nil {
		return err
	}
	hash = sha256.Sum256([]byte(approved))
	if hex.EncodeToString(hash[:]) != record.SourceSHA256 {
		return errors.New("el artefacto aprobado cambió")
	}
	hash = sha256.Sum256([]byte(record.Project + "\x00" + record.Target))
	name := "workbench-" + hex.EncodeToString(hash[:12])
	backupPath := filepath.Join(dir, record.ID+".indexed")
	if _, err := os.Lstat(backupPath); os.IsNotExist(err) {
		raw, err := m.memuMemoryCommand(ctx, []string{"list-files", "--json"}, nil)
		if err != nil {
			return err
		}
		var listing struct {
			Files []map[string]any `json:"recall_files"`
		}
		if err := json.Unmarshal(raw, &listing); err != nil {
			return err
		}
		previous := map[string]any{"name": name, "track": "skill", "description": "Withdrawn Workbench artifact", "content": "This artifact was withdrawn. Do not use or apply it."}
		for _, file := range listing.Files {
			if file["name"] == name && file["track"] == "skill" {
				previous = file
				break
			}
		}
		raw, err = json.Marshal(memuIndexBackup{Name: name, Previous: previous})
		if err != nil {
			return err
		}
		if err := writePrivateFile(backupPath, raw); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	file := map[string]any{"name": name, "track": "skill", "description": filepath.Base(record.Project) + ": " + record.Target, "content": approved}
	if _, err := m.memuMemoryCommand(ctx, []string{"commit", "-", "--json"}, map[string]any{"recall_files": []any{file}}); err != nil {
		return err
	}
	// Rollback can race the embedding request; restore instead of publishing it again.
	latest, _, err := m.memuAdoption(record.ID)
	if err != nil {
		return err
	}
	if latest.RolledBack {
		return m.restoreMemuIndexLocked(ctx, record.ID)
	}
	return nil
}

// RestoreMemuIndex restores the previous memory record after a project rollback.
func (m *Module) RestoreMemuIndex(ctx context.Context, id string) error {
	if !m.memuMu.TryLock() {
		return errors.New("memU ocupado; la memoria se reconciliará antes del siguiente lote")
	}
	defer m.memuMu.Unlock()
	return m.restoreMemuIndexLocked(ctx, id)
}
func (m *Module) restoreMemuIndexLocked(ctx context.Context, id string) error {
	record, dir, err := m.memuAdoption(id)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id+".indexed")
	raw, err := readMemuFile(path, 1<<20)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !record.RolledBack {
		return nil
	}
	var backup memuIndexBackup
	if err := json.Unmarshal([]byte(raw), &backup); err != nil {
		return err
	}
	if _, err := m.memuMemoryCommand(ctx, []string{"commit", "-", "--json"}, map[string]any{"recall_files": []any{backup.Previous}}); err != nil {
		return err
	}
	return os.Remove(path)
}
func (m *Module) reconcileMemuRollbacks(ctx context.Context, base string) error {
	paths, err := filepath.Glob(filepath.Join(base, "adoptions", "*.indexed"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := m.restoreMemuIndexLocked(ctx, strings.TrimSuffix(filepath.Base(path), ".indexed")); err != nil {
			return err
		}
	}
	return nil
}
