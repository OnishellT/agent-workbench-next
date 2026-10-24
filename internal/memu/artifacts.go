// Package memu expone la revisión/adopción/rollback de archivos generados por
// memu. Solo transporta bytes entre el área privada (STATE/memu/skill, solo
// markdown/py/sh) y destinos registrados del proyecto; nunca ejecuta nada.
//
// Destinos permitidos, relativos a una raíz de ProjectRoots:
// .agents/skills/**, scripts/** y AGENTS.md. AGENTS.md se amplía por bloques
// (preserva el texto ajeno); el resto se reemplaza. Cada adopción guarda copia
// privada en STATE/memu/adoptions y exige CAS contra lo revisado; el rollback
// solo restaura si el destino aún coincide con lo adoptado.
package memu

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Extensiones generadas que el handler acepta como fuente y destino.
var allowedExt = map[string]bool{".md": true, ".py": true, ".sh": true, ".json": true}

// Handler traslada artefactos revisados a destinos registrados.
//
// AfterRollback se invoca tras un rollback de proyecto exitoso para restaurar
// el índice de memoria (lo asigna el coordinador, p. ej. notebooks
// RestoreMemuIndex). No autoriza ni impide el undo: un fallo se reporta en la
// respuesta y se reconcilia antes del siguiente prepare.
type Handler struct {
	AfterRollback func(ctx context.Context, id string) error

	mu         sync.Mutex
	stateDir   string
	roots      func() []string
	authorized func() error
	adoptions  string
	mux        *http.ServeMux
}

// New construye el handler. stateDir es el STATE privado; roots devuelve las
// raíces de proyecto registradas; authorized valida la fuente antes de cada
// lectura/escritura (lo conecta el coordinador). Un authorized nulo deniega.
func New(stateDir string, roots func() []string, authorized func() error) *Handler {
	h := &Handler{
		stateDir:   stateDir,
		roots:      roots,
		authorized: authorized,
		adoptions:  filepath.Join(stateDir, "memu", "adoptions"),
		mux:        http.NewServeMux(),
	}
	h.mux.HandleFunc("GET /review", h.review)
	h.mux.HandleFunc("POST /adopt", h.adopt)
	h.mux.HandleFunc("POST /rollback", h.rollback)
	h.mux.HandleFunc("GET /adoptions", h.listAdoptions)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

type reviewResponse struct {
	Path         string `json:"path"`
	Project      string `json:"project"`
	Target       string `json:"target"`
	SourceSHA256 string `json:"source_sha256"`
	BeforeSHA256 string `json:"before_sha256"`
	BeforeText   string `json:"before_text"`
	AfterSHA256  string `json:"after_sha256"`
	AfterText    string `json:"after_text"`
	TargetExists bool   `json:"target_exists"`
	Append       bool   `json:"append"`
}

type adoptRequest struct {
	Path         string `json:"path"`
	Project      string `json:"project"`
	Target       string `json:"target"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

type adoptResponse struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Project   string `json:"project"`
	Target    string `json:"target"`
	Version   string `json:"after_sha256"`
	AdoptedAt string `json:"adopted_at"`
}

type rollbackRequest struct {
	ID string `json:"id"`
}

type rollbackResponse struct {
	ID                 string `json:"id"`
	Target             string `json:"target"`
	Restored           bool   `json:"restored"`
	MemoryRestoreError string `json:"memory_restore_error,omitempty"`
}

type adoptionRecord struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Project      string `json:"project"`
	Target       string `json:"target"`
	SourceSHA256 string `json:"source_sha256"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	HadTarget    bool   `json:"had_target"`
	Mode         uint32 `json:"mode"`
	AdoptedAt    string `json:"adopted_at"`
	RolledBack   bool   `json:"rolled_back"`
}

type adoptionSummary struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Project      string `json:"project"`
	Target       string `json:"target"`
	SourceSHA256 string `json:"source_sha256"`
	AfterSHA256  string `json:"after_sha256"`
	HadTarget    bool   `json:"had_target"`
	AdoptedAt    string `json:"adopted_at"`
}

// decodeBody reads one strict JSON object of at most 1 MiB.
func decodeBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (h *Handler) checkAuthorized() error {
	if h.authorized == nil {
		return errors.New("fuente no autorizada")
	}
	return h.authorized()
}

// projectRoot exige que project sea una raíz registrada, limpia y existente.
func (h *Handler) projectRoot(project string) (string, error) {
	clean := filepath.Clean(project)
	if h.roots == nil {
		return "", errors.New("sin raíces de proyecto registradas")
	}
	for _, root := range h.roots() {
		if clean == filepath.Clean(root) {
			info, err := os.Stat(clean)
			if err != nil || !info.IsDir() {
				return "", errors.New("raíz de proyecto no disponible")
			}
			return clean, nil
		}
	}
	return "", errors.New("proyecto no registrado")
}

// resolveWithin une root+rel y exige que el resultado (tras resolver symlinks)
// siga dentro de root. Rechaza absolutos, escapes y enlaces fuera.
func resolveWithin(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("ruta relativa obligatoria")
	}
	if filepath.IsAbs(rel) {
		return "", errors.New("ruta absoluta no permitida")
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == ".." {
			return "", errors.New("componente .. no permitido")
		}
	}
	joined := filepath.Join(root, filepath.Clean(string(filepath.Separator)+rel))
	inside, err := isWithin(root, joined)
	if err != nil || !inside {
		return "", errors.New("la ruta escapa de su raíz")
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", errors.New("enlace no resoluble")
		}
		// Aún no existe: resuelve el padre existente más cercano.
		parent := joined
		for {
			parent = filepath.Dir(parent)
			resolved, err = filepath.EvalSymlinks(parent)
			if err == nil {
				break
			}
			if !os.IsNotExist(err) {
				return "", errors.New("enlace no resoluble")
			}
			if parent == filepath.Dir(parent) {
				return "", errors.New("la ruta escapa de su raíz")
			}
		}
		resolved = filepath.Join(resolved, mustRel(joined, parent))
	}
	inside, err = isWithin(root, resolved)
	if err != nil || !inside {
		return "", errors.New("la ruta escapa de su raíz")
	}
	return resolved, nil
}

func mustRel(joined, parent string) string {
	rel, err := filepath.Rel(parent, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "."
	}
	return rel
}

func isWithin(root, path string) (bool, error) {
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, err
	}
	pathEval, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}
		pathEval = path
	}
	rel, err := filepath.Rel(rootEval, pathEval)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// checkDestination aplica la regla de destinos: solo .agents/skills/**,
// scripts/** y AGENTS.md, con extensiones generadas. Devuelve si es append
// (AGENTS.md) o reemplazo.
func checkDestination(target string) (appendBlock bool, err error) {
	clean := filepath.Clean(target)
	ext := strings.ToLower(filepath.Ext(clean))
	if !allowedExt[ext] {
		return false, fmt.Errorf("extensión no generada: %s", ext)
	}
	if clean == "AGENTS.md" {
		return true, nil
	}
	if clean == ".agents/skills" || strings.HasPrefix(clean, ".agents/skills"+string(filepath.Separator)) {
		return false, nil
	}
	if clean == "scripts" || strings.HasPrefix(clean, "scripts"+string(filepath.Separator)) {
		return false, nil
	}
	return false, fmt.Errorf("destino no permitido: %s", target)
}

// sourceFile resuelve un artefacto generado bajo STATE/memu/skill.
func (h *Handler) sourceFile(rel string) (string, error) {
	ext := strings.ToLower(filepath.Ext(rel))
	if !allowedExt[ext] {
		return "", fmt.Errorf("extensión no generada: %s", ext)
	}
	// The name is written inside <!-- memu:NAME --> markers in AGENTS.md.
	if strings.Contains(rel, "-->") || strings.IndexFunc(rel, unicode.IsControl) >= 0 {
		return "", errors.New("nombre de artefacto inválido")
	}
	skill := filepath.Join(h.stateDir, "memu", "skill")
	return resolveWithin(skill, rel)
}

// adoptionPaths is the validated, resolved form of the path/project/target a
// review or adoption request names.
type adoptionPaths struct {
	appendBlock bool
	source      string
	root        string
	dest        string
}

// resolveAdoption validates the request's target and resolves the artifact and
// destination files; review and adopt share it so both reject the same inputs.
func (h *Handler) resolveAdoption(rel, project, target string) (adoptionPaths, error) {
	appendBlock, err := checkDestination(target)
	if err != nil {
		return adoptionPaths{}, err
	}
	source, err := h.sourceFile(rel)
	if err != nil {
		return adoptionPaths{}, err
	}
	root, err := h.projectRoot(project)
	if err != nil {
		return adoptionPaths{}, err
	}
	dest, err := resolveWithin(root, target)
	if err != nil {
		return adoptionPaths{}, err
	}
	return adoptionPaths{appendBlock: appendBlock, source: source, root: root, dest: dest}, nil
}

// adoptedContent is what the destination holds after adoption: the artifact itself,
// or the destination with the artifact's AGENTS.md block appended.
func adoptedContent(appendBlock bool, before []byte, rel string, sourceBytes []byte) []byte {
	if appendBlock {
		return appendAgentsBlock(before, rel, sourceBytes)
	}
	return sourceBytes
}

func (h *Handler) review(w http.ResponseWriter, r *http.Request) {
	if err := h.checkAuthorized(); err != nil {
		fail(w, http.StatusForbidden, err)
		return
	}
	query := r.URL.Query()
	rel, project, target := query.Get("path"), query.Get("project"), query.Get("target")
	if rel == "" || project == "" || target == "" {
		fail(w, http.StatusBadRequest, errors.New("path, project y target son obligatorios"))
		return
	}
	resolved, err := h.resolveAdoption(rel, project, target)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	appendBlock, source, root, dest := resolved.appendBlock, resolved.source, resolved.root, resolved.dest
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		fail(w, http.StatusNotFound, errors.New("artefacto no encontrado"))
		return
	}
	before, err := os.ReadFile(dest)
	if err != nil && !os.IsNotExist(err) {
		fail(w, http.StatusConflict, errors.New("destino no legible"))
		return
	}
	final := adoptedContent(appendBlock, before, rel, sourceBytes)
	writeJSON(w, http.StatusOK, reviewResponse{
		Path:         rel,
		Project:      root,
		Target:       target,
		SourceSHA256: sha256Hex(sourceBytes),
		BeforeSHA256: sha256Hex(before),
		BeforeText:   string(before),
		AfterSHA256:  sha256Hex(final),
		AfterText:    string(final),
		TargetExists: err == nil,
		Append:       appendBlock,
	})
}

func (h *Handler) adopt(w http.ResponseWriter, r *http.Request) {
	if err := h.checkAuthorized(); err != nil {
		fail(w, http.StatusForbidden, err)
		return
	}
	var input adoptRequest
	if err := decodeBody(r, &input); err != nil {
		fail(w, http.StatusBadRequest, errors.New("adopción inválida"))
		return
	}
	if input.Path == "" || input.Project == "" || input.Target == "" ||
		input.BeforeSHA256 == "" || input.AfterSHA256 == "" {
		fail(w, http.StatusBadRequest, errors.New("path, project, target y hashes son obligatorios"))
		return
	}
	h.mu.Lock() // the body is already read: a slow client cannot hold the lock
	defer h.mu.Unlock()
	resolved, err := h.resolveAdoption(input.Path, input.Project, input.Target)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	appendBlock, source, root, dest := resolved.appendBlock, resolved.source, resolved.root, resolved.dest
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		fail(w, http.StatusNotFound, errors.New("artefacto no encontrado"))
		return
	}
	before, err := os.ReadFile(dest)
	hadTarget := true
	if err != nil {
		if !os.IsNotExist(err) {
			fail(w, http.StatusConflict, errors.New("destino no legible"))
			return
		}
		hadTarget = false
		before = nil
	}
	if sha256Hex(before) != input.BeforeSHA256 {
		fail(w, http.StatusConflict, errors.New("el destino cambió desde la revisión; revísalo de nuevo"))
		return
	}
	next := adoptedContent(appendBlock, before, input.Path, sourceBytes)
	if sha256Hex(next) != input.AfterSHA256 {
		fail(w, http.StatusConflict, errors.New("el artefacto cambió desde la revisión"))
		return
	}
	mode := os.FileMode(0644)
	if strings.ToLower(filepath.Ext(dest)) == ".sh" {
		mode = 0755
	}
	if hadTarget {
		if info, err := os.Stat(dest); err == nil {
			mode = info.Mode().Perm()
		}
	}
	id, err := newID()
	if err != nil {
		fail(w, http.StatusInternalServerError, errors.New("no se pudo registrar la adopción"))
		return
	}
	// Recibo y respaldo ANTES de tocar el destino: si el JSON falla no hay
	// nada adoptado. El snapshot .approved guarda SOLO el artefacto aprobado
	// (nunca propuestas rechazadas ni texto ajeno de AGENTS.md) para indexar
	// lo adoptado y para un /commit explícito futuro.
	record := adoptionRecord{
		ID: id, Path: input.Path, Project: root, Target: input.Target,
		SourceSHA256: sha256Hex(sourceBytes),
		BeforeSHA256: input.BeforeSHA256, AfterSHA256: input.AfterSHA256,
		HadTarget: hadTarget, Mode: uint32(mode), AdoptedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := os.MkdirAll(h.adoptions, 0700); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("historial no disponible"))
		return
	}
	receipt := filepath.Join(h.adoptions, id+".json")
	backup := filepath.Join(h.adoptions, id+".before")
	approved := filepath.Join(h.adoptions, id+".approved")
	evidence := filepath.Join(h.adoptions, id+".evidence")
	raw, _ := json.Marshal(record)
	if err := os.WriteFile(receipt, raw, 0600); err != nil {
		fail(w, http.StatusInternalServerError, errors.New("no se pudo registrar la adopción"))
		return
	}
	cleanup := func() {
		_ = os.Remove(receipt)
		_ = os.Remove(backup)
		_ = os.Remove(approved)
		_ = os.Remove(evidence)
	}
	if err := os.WriteFile(approved, sourceBytes, 0600); err != nil {
		cleanup()
		fail(w, http.StatusInternalServerError, errors.New("no se pudo guardar el artefacto aprobado"))
		return
	}
	// Copia del manifest vigente al adoptar: el commit valida las fuentes
	// ORIGINALES aunque otro prepare las rote después. Se omite si no hay
	// (handler standalone de ensayo); el authorized real ya exige manifest.
	if err := snapshotEvidence(h.stateDir, evidence); err != nil {
		cleanup()
		fail(w, http.StatusInternalServerError, errors.New("no se pudo guardar la evidencia de fuentes"))
		return
	}
	if hadTarget {
		if err := os.WriteFile(backup, before, 0600); err != nil {
			cleanup()
			fail(w, http.StatusInternalServerError, errors.New("no se pudo respaldar el destino"))
			return
		}
	}
	if err := writeAtomic(dest, next, mode); err != nil {
		// Se conserva recibo+aprobado+evidencia para diagnóstico y reintento;
		// el respaldo sin destino cambiado no aporta y se retira. El listado
		// excluye este id (el hash del destino no coincide) y el rollback
		// responde 409 sin tocar nada.
		_ = os.Remove(backup)
		fail(w, http.StatusConflict, errors.New("no se pudo adoptar el destino"))
		return
	}
	writeJSON(w, http.StatusOK, adoptResponse{
		ID: id, Path: input.Path, Project: root, Target: input.Target,
		Version: input.AfterSHA256, AdoptedAt: record.AdoptedAt,
	})
}

// listAdoptions lista recibos válidos para rollback tras recarga/reinicio. No exige
// la fuente vigente: bastan la raíz registrada y que el destino aún coincida
// con el hash adoptado. Sin textos ni respaldos, solo el sobre del recibo.
func (h *Handler) listAdoptions(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.adoptions)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []adoptionSummary{})
			return
		}
		fail(w, http.StatusConflict, errors.New("historial no legible"))
		return
	}
	out := []adoptionSummary{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(h.adoptions, name))
		if err != nil {
			continue
		}
		var record adoptionRecord
		if err := json.Unmarshal(raw, &record); err != nil || record.RolledBack {
			continue
		}
		if _, err := checkDestination(record.Target); err != nil {
			continue
		}
		root, err := h.projectRoot(record.Project)
		if err != nil {
			continue
		}
		dest, err := resolveWithin(root, record.Target)
		if err != nil {
			continue
		}
		current, err := os.ReadFile(dest)
		if err != nil || sha256Hex(current) != record.AfterSHA256 {
			continue
		}
		out = append(out, adoptionSummary{
			ID: record.ID, Path: record.Path, Project: root, Target: record.Target,
			SourceSHA256: record.SourceSHA256, AfterSHA256: record.AfterSHA256,
			HadTarget: record.HadTarget, AdoptedAt: record.AdoptedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	// El undo no depende del consentimiento de fuentes: bastan la raíz
	// registrada, el recibo y el hash. No se toca configuración de modelos.
	var input rollbackRequest
	if err := decodeBody(r, &input); err != nil || input.ID == "" {
		fail(w, http.StatusBadRequest, errors.New("id de adopción obligatorio"))
		return
	}
	response, status, err := h.rollbackLocked(input)
	if err != nil && !response.Restored {
		fail(w, status, err)
		return
	}
	// El archivo ya quedó restaurado: el hook de memoria no lo oculta ni lo
	// revierte; su fallo se reporta y se reconcilia antes del siguiente prepare.
	// Corre sin h.mu: puede tardar y no debe bloquear adopciones.
	if h.AfterRollback != nil {
		if hookErr := h.AfterRollback(r.Context(), response.ID); hookErr != nil {
			response.MemoryRestoreError = hookErr.Error()
		}
	}
	if err != nil { // restaurado, pero el recibo no quedó marcado
		fail(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// rollbackLocked restaura el destino bajo h.mu. Si el destino se restauró pero el
// recibo no pudo marcarse, devuelve la respuesta con Restored y el error.
func (h *Handler) rollbackLocked(input rollbackRequest) (rollbackResponse, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.ContainsAny(input.ID, "/\\") || strings.Contains(input.ID, "..") {
		return rollbackResponse{}, http.StatusBadRequest, errors.New("id de adopción inválido")
	}
	raw, err := os.ReadFile(filepath.Join(h.adoptions, input.ID+".json"))
	if err != nil {
		return rollbackResponse{}, http.StatusNotFound, errors.New("adopción no encontrada")
	}
	var record adoptionRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return rollbackResponse{}, http.StatusConflict, errors.New("registro de adopción inválido")
	}
	if record.RolledBack {
		return rollbackResponse{}, http.StatusConflict, errors.New("la adopción ya se revirtió")
	}
	root, err := h.projectRoot(record.Project)
	if err != nil {
		return rollbackResponse{}, http.StatusBadRequest, err
	}
	appendBlock, err := checkDestination(record.Target)
	if err != nil {
		return rollbackResponse{}, http.StatusBadRequest, err
	}
	dest, err := resolveWithin(root, record.Target)
	if err != nil {
		return rollbackResponse{}, http.StatusBadRequest, err
	}
	current, err := os.ReadFile(dest)
	if err != nil {
		return rollbackResponse{}, http.StatusConflict, errors.New("destino no legible")
	}
	// El recibo guarda el hash final adoptado: el undo no necesita la fuente.
	if sha256Hex(current) != record.AfterSHA256 {
		return rollbackResponse{}, http.StatusConflict, errors.New("el destino ya no coincide con lo adoptado; no se revierte a ciegas")
	}
	if record.HadTarget {
		backup, err := os.ReadFile(filepath.Join(h.adoptions, record.ID+".before"))
		if err != nil {
			return rollbackResponse{}, http.StatusConflict, errors.New("respaldo no disponible")
		}
		if err := writeAtomic(dest, backup, os.FileMode(record.Mode)); err != nil {
			return rollbackResponse{}, http.StatusConflict, errors.New("no se pudo restaurar el destino")
		}
	} else if appendBlock {
		plain, err := os.ReadFile(dest)
		if err != nil {
			return rollbackResponse{}, http.StatusConflict, errors.New("destino no legible")
		}
		if err := writeAtomic(dest, removeAgentsBlock(plain, record.Path), os.FileMode(record.Mode)); err != nil {
			return rollbackResponse{}, http.StatusConflict, errors.New("no se pudo retirar el bloque")
		}
	} else if err := os.Remove(dest); err != nil {
		return rollbackResponse{}, http.StatusConflict, errors.New("no se pudo retirar el destino")
	}
	record.RolledBack = true
	response := rollbackResponse{ID: record.ID, Target: record.Target, Restored: true}
	raw, _ = json.Marshal(record)
	if err := os.WriteFile(filepath.Join(h.adoptions, record.ID+".json"), raw, 0600); err != nil {
		return response, http.StatusInternalServerError, errors.New("destino restaurado pero no se pudo marcar el recibo")
	}
	return response, http.StatusOK, nil
}

// appendAgentsBlock agrega (o refresca) el bloque de un artefacto al final de
// AGENTS.md, preservando todo el texto ajeno.
func appendAgentsBlock(current []byte, rel string, after []byte) []byte {
	block := agentsBlock(rel, after)
	plain := removeAgentsBlock(current, rel)
	if len(bytes.TrimSpace(plain)) == 0 {
		return block
	}
	separator := ""
	if len(plain) > 0 && !strings.HasSuffix(string(plain), "\n") {
		separator = "\n"
	}
	return []byte(string(plain) + separator + string(block))
}

func agentsBlock(rel string, after []byte) []byte {
	var b strings.Builder
	b.WriteString("<!-- memu:" + rel + " -->\n")
	b.Write(after)
	if !strings.HasSuffix(string(after), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("<!-- /memu:" + rel + " -->\n")
	return []byte(b.String())
}

func removeAgentsBlock(current []byte, rel string) []byte {
	text := string(current)
	open := "<!-- memu:" + rel + " -->"
	close := "<!-- /memu:" + rel + " -->"
	for {
		start := strings.Index(text, open)
		if start < 0 {
			break
		}
		rest := text[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			break
		}
		text = text[:start] + rest[end+len(close):]
	}
	return []byte(strings.TrimRight(text, "\n") + "\n")
}

// snapshotEvidence copia STATE/memu/export/manifest.json al adoptar, para que
// el commit valide las fuentes originales del recibo aunque otro prepare las
// rote después. Omite si no hay manifest, es symlink/directorio o supera 1MiB;
// falla si existe pero no se puede leer.
func snapshotEvidence(stateDir, dest string) error {
	const maxManifest = 1 << 20
	path := filepath.Join(stateDir, "memu", "export", "manifest.json")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxManifest {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifest+1))
	if err != nil {
		return err
	}
	if len(data) > maxManifest {
		return nil
	}
	return os.WriteFile(dest, data, 0600)
}

// writeAtomic escribe por renombre atómico y fija el modo pedido.
func writeAtomic(dest string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".memu-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, dest)
}
