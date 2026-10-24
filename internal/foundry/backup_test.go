// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

// FO-21: la copia es la base SQLite real, sin tocar la original y sin compartir archivo.
func TestLearningBackupCopiesTheDatabaseWithReceipt(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "consented-backup")
	if err := os.MkdirAll(sourceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONLFixture(t, filepath.Join(sourceRoot, "session.jsonl"),
		map[string]any{"session_id": "backup-session", "role": "user", "kind": "message", "text": "petición humana de la copia"},
	)
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/conversations/sources", map[string]any{"path": sourceRoot, "adapter": "canonical"})
	if status != http.StatusCreated {
		t.Fatalf("register source status=%d body=%+v", status, body)
	}
	if job := queueFoundryScan(t, client, server.URL, foundryObject(t, body["source"])["id"].(string)); job["status"] != "succeeded" {
		t.Fatalf("scan of the backup fixture: %+v", job)
	}
	database := filepath.Join(root, "state", "foundry", "foundry.sqlite")
	beforeDigest, beforeSize, err := fileSha256(database)
	if err != nil {
		t.Fatal(err)
	}
	var wantedSessions, wantedVersions int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&wantedSessions); err != nil {
		t.Fatal(err)
	}
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM versions`).Scan(&wantedVersions); err != nil {
		t.Fatal(err)
	}

	status, body = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/backup", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("backup status=%d body=%+v", status, body)
	}
	path, digest := fmt.Sprint(body["path"]), fmt.Sprint(body["sha256"])
	if !strings.HasPrefix(path, filepath.Join(root, "state", "foundry", "backups")+string(os.PathSeparator)) || len(digest) != 64 {
		t.Fatalf("receipt does not name a real private copy: %+v", body)
	}
	if fmt.Sprint(body["versions"]) != fmt.Sprint(wantedVersions) || body["bytes"] == float64(0) || body["created"] == nil || body["id"] == nil {
		t.Fatalf("receipt does not report the copy: %+v (versions wanted %d)", body, wantedVersions)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("copy permissions = %o, want owner-only", info.Mode().Perm())
	}
	if digest != mustDigest(t, path) {
		t.Fatalf("receipt hash does not match the file: %s", path)
	}
	header := make([]byte, 16)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Read(header); err != nil {
		t.Fatal(err)
	}
	file.Close()
	if string(header) != "SQLite format 3\x00" {
		t.Fatalf("copy is not a SQLite database: %q", string(header))
	}
	// La copia abre como base y la original no cambió.
	copyDB, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var copiedSessions int
	if err := copyDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&copiedSessions); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := copyDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("copy integrity=%q err=%v", integrity, err)
	}
	if copiedSessions != wantedSessions {
		t.Fatalf("copy holds %d sessions, want %d", copiedSessions, wantedSessions)
	}
	afterDigest, afterSize, err := fileSha256(database)
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest != beforeDigest || afterSize != beforeSize {
		t.Fatalf("the original database changed during the copy")
	}

	// Dos copias a la vez conservan su propio archivo, y cada una responde por su recibo: lo
	// que contienen puede diferir (una copia puede incluir el recibo de la otra) y eso no es
	// un fallo, así que se comprueba cada archivo contra sí mismo.
	receipts := make([]map[string]any, 2)
	var wg sync.WaitGroup
	for index := range receipts {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			status, answer := callFoundryJSON(t, server.Client(), server.URL, http.MethodPost, "/api/foundry/backup", map[string]any{})
			if status != http.StatusCreated {
				t.Errorf("concurrent backup status=%d body=%+v", status, answer)
				return
			}
			receipts[slot] = answer
		}(index)
	}
	wg.Wait()
	first, second := receipts[0], receipts[1]
	if first == nil || second == nil {
		t.Fatalf("one concurrent copy did not answer: %+v", receipts)
	}
	if first["path"] == nil || first["path"] == second["path"] || first["id"] == second["id"] {
		t.Fatalf("concurrent copies must keep their own file and receipt: %+v %+v", first, second)
	}
	for _, answer := range receipts {
		candidate := fmt.Sprint(answer["path"])
		digest, size, err := fileSha256(candidate)
		if err != nil {
			t.Fatalf("concurrent copy %s is unreadable: %v", candidate, err)
		}
		if digest != fmt.Sprint(answer["sha256"]) || size != int64(answer["bytes"].(float64)) {
			t.Fatalf("concurrent copy %s does not match its own receipt: %+v", candidate, answer)
		}
		copyDB, err := sql.Open("sqlite", "file:"+candidate+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		var integrity string
		if err := copyDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
			copyDB.Close()
			t.Fatalf("concurrent copy %s integrity=%q err=%v", candidate, integrity, err)
		}
		var sessions int
		if err := copyDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != wantedSessions {
			copyDB.Close()
			t.Fatalf("concurrent copy %s holds %d sessions (%v), want %d", candidate, sessions, err, wantedSessions)
		}
		copyDB.Close()
	}
}

// FO-21: la descarga sirve sólo el archivo que un recibo nombró y declara sus fallos.
func TestLearningBackupDownloadGuards(t *testing.T) {
	root := t.TempDir()
	module, server := newFoundryTestServer(t, filepath.Join(root, "state"), nil)
	client := server.Client()
	status, body := callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/backup", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("backup status=%d body=%+v", status, body)
	}
	id, path := fmt.Sprint(body["id"]), fmt.Sprint(body["path"])
	backupsDir := filepath.Join(root, "state", "foundry", "backups")

	response, err := client.Get(server.URL + "/api/foundry/backup/" + id)
	if err != nil {
		t.Fatal(err)
	}
	downloaded := make([]byte, 0)
	buffer := make([]byte, 4096)
	for {
		read, readErr := response.Body.Read(buffer)
		downloaded = append(downloaded, buffer[:read]...)
		if readErr != nil {
			break
		}
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/vnd.sqlite3" {
		t.Fatalf("download status=%d type=%s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if !strings.Contains(response.Header.Get("Content-Disposition"), filepath.Base(path)) {
		t.Fatalf("download does not name the copy: %s", response.Header.Get("Content-Disposition"))
	}
	if int64(len(downloaded)) != int64(body["bytes"].(float64)) || digestBytes(downloaded) != fmt.Sprint(body["sha256"]) {
		t.Fatalf("download does not deliver the receipted file")
	}

	status, answer := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/backup/9999", nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown copy expected 404, observed %d %+v", status, answer)
	}
	foundryErrorCode(t, answer, "backup_not_found")
	status, answer = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/backup/not-a-number", nil)
	if status != http.StatusNotFound {
		t.Fatalf("non numeric id expected 404, observed %d %+v", status, answer)
	}
	status, answer = callFoundryJSON(t, client, server.URL, http.MethodPut, "/api/foundry/backup/"+id, map[string]any{})
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method expected 405, observed %d %+v", status, answer)
	}

	// Fuera del directorio, symlink o archivo cambiado: no se sirve.
	outside := filepath.Join(root, "outside.sqlite")
	if err := os.WriteFile(outside, []byte("SQLite format 3\x00 fuera"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(backupsDir, "linked.sqlite")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	outsideDigest, outsideSize, err := fileSha256(outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []struct{ path, code string }{
		{outside, "backup_not_found"},
		{link, "backup_not_found"},
	} {
		insert, err := module.db.Exec(`INSERT INTO learning_backup_receipts(path,sha256,bytes,versions,created) VALUES(?,?,?,?,?)`, candidate.path, outsideDigest, outsideSize, 0, nowSeconds())
		if err != nil {
			t.Fatal(err)
		}
		seededID, err := insert.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		status, answer = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/backup/"+fmt.Sprint(seededID), nil)
		if status != http.StatusNotFound {
			t.Fatalf("path %s expected 404, observed %d %+v", candidate.path, status, answer)
		}
		foundryErrorCode(t, answer, candidate.code)
	}
	copiedDigest := mustDigest(t, path)
	insert, err := module.db.Exec(`INSERT INTO learning_backup_receipts(path,sha256,bytes,versions,created) VALUES(?,?,?,?,?)`, path, copiedDigest, 1, 0, nowSeconds())
	if err != nil {
		t.Fatal(err)
	}
	changedID, err := insert.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("cambiado")); err != nil {
		t.Fatal(err)
	}
	file.Close()
	status, answer = callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/backup/"+fmt.Sprint(changedID), nil)
	if status != http.StatusConflict {
		t.Fatalf("a changed copy expected 409, observed %d %+v", status, answer)
	}
	foundryErrorCode(t, answer, "backup_changed")

	// Un fallo no es copia ni deja recibo o parcial.
	var receiptsBefore, filesBefore int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM learning_backup_receipts`).Scan(&receiptsBefore); err != nil {
		t.Fatal(err)
	}
	filesBefore = countBackupFiles(t, backupsDir)
	if err := os.Chmod(backupsDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backupsDir, 0700) })
	status, answer = callFoundryJSON(t, client, server.URL, http.MethodPost, "/api/foundry/backup", map[string]any{})
	if status == http.StatusCreated {
		t.Fatalf("a copy that could not be written was reported as success: %+v", answer)
	}
	var receiptsAfter int
	if err := module.db.QueryRow(`SELECT COUNT(*) FROM learning_backup_receipts`).Scan(&receiptsAfter); err != nil {
		t.Fatal(err)
	}
	if receiptsAfter != receiptsBefore || countBackupFiles(t, backupsDir) != filesBefore {
		t.Fatalf("a failed copy left a receipt or a partial file behind")
	}
}

func countBackupFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sqlite") {
			count++
		}
	}
	return count
}

func mustDigest(t *testing.T, path string) string {
	t.Helper()
	digest, _, err := fileSha256(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
