// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FO-21: copia propia de la base SQLite; snapshots y artefactos van aparte.
func (m *Module) createLearningBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	dir := filepath.Join(m.stateDir, "foundry", "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	// Nombre exclusivo como mkstemp: el archivo final es la reserva, vacío, y dos copias
	// simultáneas nunca comparten ruta.
	suffix, err := newID("")
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	final := filepath.Join(dir, fmt.Sprintf("foundry-%d-%s.sqlite", time.Now().Unix(), suffix))
	reserved, err := os.OpenFile(final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	conn, err := m.db.Conn(r.Context())
	if err != nil {
		_ = reserved.Close()
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	// VACUUM INTO admite el destino vacío ya reservado y la misma conexión cuenta las
	// versiones que la copia contiene.
	if _, err := conn.ExecContext(r.Context(), `VACUUM INTO ?`, final); err != nil {
		_ = reserved.Close()
		_ = conn.Close()
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	var versions int
	if err := conn.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM versions`).Scan(&versions); err != nil {
		_ = reserved.Close()
		_ = conn.Close()
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	if err := conn.Close(); err != nil {
		_ = reserved.Close()
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	if err := reserved.Close(); err != nil {
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	digest, size, err := fileSha256(final)
	if err != nil {
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	created := nowSeconds()
	insert, err := m.db.ExecContext(r.Context(), `INSERT INTO learning_backup_receipts(path,sha256,bytes,versions,created) VALUES(?,?,?,?,?)`, final, digest, size, versions, created)
	if err != nil {
		_ = os.Remove(final)
		writeAPIError(w, internalError(err))
		return
	}
	id, err := insert.LastInsertId()
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "path": final, "sha256": digest, "bytes": size, "versions": versions, "created": created})
}

// Descarga de la copia con recibo: un solo descriptor se valida, se hashea y se sirve.
func (m *Module) learningBackupDownload(w http.ResponseWriter, r *http.Request, rawID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
	if err != nil || id < 1 {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	var storedPath, digest string
	var storedSize int64
	err = m.db.QueryRowContext(r.Context(), `SELECT path,sha256,bytes FROM learning_backup_receipts WHERE id=?`, id).Scan(&storedPath, &digest, &storedSize)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	dir := filepath.Join(m.stateDir, "foundry", "backups")
	clean := filepath.Clean(storedPath)
	name := filepath.Base(clean)
	if filepath.Dir(clean) != filepath.Clean(dir) || strings.ContainsAny(name, `/\`+"\x00\r\n\"") {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	// El mismo descriptor que se valida es el que se sirve: un reemplazo de la ruta no
	// cambia los bytes entregados.
	if err := validateNoSymlinkComponents(dir, clean); err != nil {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	before, err := os.Lstat(clean)
	if err != nil || !before.Mode().IsRegular() {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	file, err := os.Open(clean)
	if err != nil {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		writeAPIError(w, &APIError{Status: http.StatusNotFound, Code: "backup_not_found", Message: "No se encontró esa copia."})
		return
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	if size != storedSize || hex.EncodeToString(hash.Sum(nil)) != digest {
		writeAPIError(w, &APIError{Status: http.StatusConflict, Code: "backup_changed", Message: "La copia cambió desde su recibo; no se sirve un archivo distinto."})
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeAPIError(w, internalError(err))
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func fileSha256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
