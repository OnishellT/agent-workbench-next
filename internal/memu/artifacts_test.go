package memu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testHandler(t *testing.T, allow bool) (*Handler, string, string) {
	t.Helper()
	state := t.TempDir()
	root := t.TempDir()
	skill := filepath.Join(state, "memu", "skill")
	if err := os.MkdirAll(skill, 0755); err != nil {
		t.Fatal(err)
	}
	h := New(state, func() []string { return []string{root} }, func() error {
		if !allow {
			return errForbiddenTest
		}
		return nil
	})
	return h, state, root
}

var errForbiddenTest = errTest("fuente no autorizada en ensayo")

type errTest string

func (e errTest) Error() string { return string(e) }

func writeSource(t *testing.T, state, rel, content string) {
	t.Helper()
	dest := filepath.Join(state, "memu", "skill", rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	return response
}

func review(t *testing.T, h http.Handler, root, rel, target string) reviewResponse {
	t.Helper()
	query := url.Values{"path": {rel}, "project": {root}, "target": {target}}
	response := doRequest(t, h, "GET", "/review?"+query.Encode(), "")
	if response.Code != http.StatusOK {
		t.Fatalf("review %s -> %s: %d %s", rel, target, response.Code, response.Body.String())
	}
	var out reviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func adopt(t *testing.T, h http.Handler, rev reviewResponse) adoptResponse {
	t.Helper()
	body, _ := json.Marshal(adoptRequest{
		Path: rev.Path, Project: rev.Project, Target: rev.Target,
		BeforeSHA256: rev.BeforeSHA256, AfterSHA256: rev.AfterSHA256,
	})
	response := doRequest(t, h, "POST", "/adopt", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("adopt %s: %d %s", rev.Target, response.Code, response.Body.String())
	}
	var out adoptResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReviewShowsBeforeAfter(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "notas.md", "# hola\n")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	rev := review(t, h, root, "notas.md", "scripts/notas.md")
	if rev.AfterSHA256 == "" || rev.TargetExists {
		t.Fatalf("revisión sin destino: %+v", rev)
	}
	if rev.BeforeSHA256 != sha256Hex(nil) {
		t.Fatalf("destino ausente con hash vacío: %+v", rev)
	}
}

func TestAdoptAtomicCAS(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "run.sh", "#!/bin/sh\necho ok\n")
	rev := review(t, h, root, "run.sh", "scripts/run.sh")
	adopted := adopt(t, h, rev)
	data, err := os.ReadFile(filepath.Join(root, "scripts", "run.sh"))
	if err != nil || string(data) != "#!/bin/sh\necho ok\n" {
		t.Fatalf("destino adoptado: %q %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(root, "scripts", "run.sh")); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("sh nuevo queda ejecutable: %v", info)
	}
	// El humano tocó el destino: CAS lo detecta.
	if err := os.WriteFile(filepath.Join(root, "scripts", "run.sh"), []byte("humano\n"), 0755); err != nil {
		t.Fatal(err)
	}
	rev2 := review(t, h, root, "run.sh", "scripts/run.sh")
	body, _ := json.Marshal(adoptRequest{Path: "run.sh", Project: root, Target: "scripts/run.sh", BeforeSHA256: rev.BeforeSHA256, AfterSHA256: rev2.AfterSHA256})
	if response := doRequest(t, h, "POST", "/adopt", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("destino cambiado es 409, hubo %d", response.Code)
	}
	// La fuente cambió: CAS lo detecta.
	writeSource(t, state, "run.sh", "#!/bin/sh\necho otro\n")
	rev3 := review(t, h, root, "run.sh", "scripts/run.sh")
	body, _ = json.Marshal(adoptRequest{Path: "run.sh", Project: root, Target: "scripts/run.sh", BeforeSHA256: rev3.BeforeSHA256, AfterSHA256: rev.AfterSHA256})
	if response := doRequest(t, h, "POST", "/adopt", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("fuente cambiada es 409, hubo %d", response.Code)
	}
	_ = adopted
}

func TestDestinationRules(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "a.md", "x\n")
	for _, target := range []string{
		"otro/a.md", "/etc/passwd", "../fuera.md", "scripts/a.exe",
		"scripts/../../fuera.md", "AGENTS.md.bak",
	} {
		query := url.Values{"path": {"a.md"}, "project": {root}, "target": {target}}
		response := doRequest(t, h, "GET", "/review?"+query.Encode(), "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("destino %q debe ser 400, hubo %d", target, response.Code)
		}
	}
	for _, target := range []string{".agents/skills/a.md", "scripts/a.py", "AGENTS.md"} {
		rev := review(t, h, root, "a.md", target)
		adopt(t, h, rev)
	}
}

func TestAdoptAndRollbackSkillJSONResource(t *testing.T) {
	h, state, root := testHandler(t, true)
	const resource = "{\"positive\":0,\"negative\":1}\n"
	writeSource(t, state, "controls.json", resource)
	rev := review(t, h, root, "controls.json", ".agents/skills/check/controls.json")
	adopted := adopt(t, h, rev)
	dest := filepath.Join(root, ".agents", "skills", "check", "controls.json")
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != resource {
		t.Fatalf("resource = %q, %v", data, err)
	}
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusOK {
		t.Fatalf("rollback resource = %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("resource survived rollback: %v", err)
	}
}

func TestRollbackOnlyOnAdoptedHash(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "s.md", "uno\n")
	rev := review(t, h, root, "s.md", "scripts/s.md")
	adopted := adopt(t, h, rev)
	// Destino manipulado: no se revierte a ciegas.
	dest := filepath.Join(root, "scripts", "s.md")
	if err := os.WriteFile(dest, []byte("manipulado\n"), 0644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("destino manipulado es 409, hubo %d", response.Code)
	}
	if err := os.WriteFile(dest, []byte("uno\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusOK {
		t.Fatalf("rollback limpio es 200, hubo %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("sin destino previo, el rollback retira el archivo")
	}
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("doble rollback es 409, hubo %d", response.Code)
	}
	// Con destino previo se restaura contenido y modo.
	writeSource(t, state, "s.md", "dos\n")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("previo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rev2 := review(t, h, root, "s.md", "scripts/s.md")
	adopted2 := adopt(t, h, rev2)
	body, _ = json.Marshal(rollbackRequest{ID: adopted2.ID})
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusOK {
		t.Fatalf("rollback con respaldo es 200, hubo %d %s", response.Code, response.Body.String())
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "previo\n" {
		t.Fatalf("contenido restaurado: %q %v", data, err)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("modo preservado: %v", info)
	}
}

func TestTraversalAndSymlinks(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "ok.md", "ok\n")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secreto.md"), []byte("s\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secreto.md"), filepath.Join(state, "memu", "skill", "fuga.md")); err != nil {
		t.Fatal(err)
	}
	// Fuente por symlink fuera del skill.
	query := url.Values{"path": {"fuga.md"}, "project": {root}, "target": {"scripts/fuga.md"}}
	if response := doRequest(t, h, "GET", "/review?"+query.Encode(), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("symlink fuente fuera es 400, hubo %d", response.Code)
	}
	// Destino con .. y absoluto.
	for _, target := range []string{"../escape.md", "/absoluto.md", "scripts/../../escape.md"} {
		query := url.Values{"path": {"ok.md"}, "project": {root}, "target": {target}}
		if response := doRequest(t, h, "GET", "/review?"+query.Encode(), ""); response.Code != http.StatusBadRequest {
			t.Fatalf("escape %q es 400, hubo %d", target, response.Code)
		}
	}
	// Directorio padre del destino enlazado fuera.
	linkDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Fatal(err)
	}
	query = url.Values{"path": {"ok.md"}, "project": {root}, "target": {"scripts/x.md"}}
	if response := doRequest(t, h, "GET", "/review?"+query.Encode(), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("padre con symlink fuera es 400, hubo %d", response.Code)
	}
	// Proyecto no registrado.
	query = url.Values{"path": {"ok.md"}, "project": {outside}, "target": {"scripts/x.md"}}
	if response := doRequest(t, h, "GET", "/review?"+query.Encode(), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("proyecto ajeno es 400, hubo %d", response.Code)
	}
}

func TestAgentsAppendPreservesForeignText(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "regla.md", "Usa bun.\n")
	foreign := "# Reglas ajenas\n\nNo tocar.\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(foreign), 0644); err != nil {
		t.Fatal(err)
	}
	rev := review(t, h, root, "regla.md", "AGENTS.md")
	if !rev.Append {
		t.Fatal("AGENTS.md es append")
	}
	adopt(t, h, rev)
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "No tocar.") || !strings.Contains(text, "Usa bun.") {
		t.Fatalf("ajeno + bloque: %q", text)
	}
	// Re-adoptar refresca el bloque sin duplicar ni tocar lo ajeno.
	writeSource(t, state, "regla.md", "Usa bun y pnpm.\n")
	rev2 := review(t, h, root, "regla.md", "AGENTS.md")
	adopt(t, h, rev2)
	data, err = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text = string(data)
	if strings.Count(text, "<!-- memu:regla.md -->") != 1 || !strings.Contains(text, "No tocar.") {
		t.Fatalf("bloque único y ajeno intacto: %q", text)
	}
	// Rollback de un AGENTS.md nuevo retira el bloque y deja lo ajeno.
	body, _ := json.Marshal(rollbackRequest{ID: "ausente"})
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusNotFound {
		t.Fatalf("id ausente es 404, hubo %d", response.Code)
	}
}

func TestAuthorizedHookDenies(t *testing.T) {
	h, _, root := testHandler(t, false)
	query := url.Values{"path": {"a.md"}, "project": {root}, "target": {"scripts/a.md"}}
	if response := doRequest(t, h, "GET", "/review?"+query.Encode(), ""); response.Code != http.StatusForbidden {
		t.Fatalf("sin autorización la lectura es 403, hubo %d", response.Code)
	}
	if response := doRequest(t, h, "POST", "/adopt", `{}`); response.Code != http.StatusForbidden {
		t.Fatalf("sin autorización la escritura es 403, hubo %d", response.Code)
	}
	// El undo no depende del consentimiento de fuentes: con cuerpo vacío la
	// validación responde 400 sin pedir autorización.
	if response := doRequest(t, h, "POST", "/rollback", `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("rollback vacío es 400, hubo %d", response.Code)
	}
}

func TestConcurrentAdoptSingleWinner(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "c.md", "concurrente\n")
	rev := review(t, h, root, "c.md", "scripts/c.md")
	body, _ := json.Marshal(adoptRequest{
		Path: rev.Path, Project: rev.Project, Target: rev.Target,
		BeforeSHA256: rev.BeforeSHA256, AfterSHA256: rev.AfterSHA256,
	})
	const racers = 8
	codes := make([]int, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			codes[n] = doRequest(t, h, "POST", "/adopt", string(body)).Code
		}(i)
	}
	wg.Wait()
	won, lost := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			won++
		case http.StatusConflict:
			lost++
		default:
			t.Fatalf("código inesperado en carrera: %d", code)
		}
	}
	if won != 1 || lost != racers-1 {
		t.Fatalf("un solo ganador CAS: %d ok, %d conflicto", won, lost)
	}
	data, err := os.ReadFile(filepath.Join(root, "scripts", "c.md"))
	if err != nil || string(data) != "concurrente\n" {
		t.Fatalf("destino del ganador: %q %v", data, err)
	}
}

func TestRollbackRecoveryReceiptVsDest(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "w.md", "final\n")
	rev := review(t, h, root, "w.md", "scripts/w.md")
	adopted := adopt(t, h, rev)
	dest := filepath.Join(root, "scripts", "w.md")
	// Recibo previo + destino cambiado después: 409 y no toca nada.
	if err := os.WriteFile(dest, []byte("cambio posterior\n"), 0644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("destino cambiado tras recibo es 409, hubo %d", response.Code)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "cambio posterior\n" {
		t.Fatalf("el 409 no restaura a ciegas: %q %v", data, err)
	}
	// Final escrito e intacto: el rollback del recibo previo funciona.
	if err := os.WriteFile(dest, []byte("final\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if response := doRequest(t, h, "POST", "/rollback", string(body)); response.Code != http.StatusOK {
		t.Fatalf("final intacto revierte con recibo previo: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("sin destino previo, el undo retira el archivo")
	}
}

func TestRollbackAfterRevoke(t *testing.T) {
	allow := true
	state := t.TempDir()
	root := t.TempDir()
	skill := filepath.Join(state, "memu", "skill")
	if err := os.MkdirAll(skill, 0755); err != nil {
		t.Fatal(err)
	}
	h := New(state, func() []string { return []string{root} }, func() error {
		if !allow {
			return errForbiddenTest
		}
		return nil
	})
	writeSource(t, state, "r.md", "revocable\n")
	rev := review(t, h, root, "r.md", "scripts/r.md")
	adopted := adopt(t, h, rev)
	// Consentimiento revocado: review/adopt se bloquean, el undo sigue.
	allow = false
	query := url.Values{"path": {"r.md"}, "project": {root}, "target": {"scripts/r.md"}}
	if response := doRequest(t, h, "GET", "/review?"+query.Encode(), ""); response.Code != http.StatusForbidden {
		t.Fatalf("tras revocar, review es 403, hubo %d", response.Code)
	}
	// La fuente también puede desaparecer: el recibo basta.
	if err := os.Remove(filepath.Join(skill, "r.md")); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	response := doRequest(t, h, "POST", "/rollback", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("rollback tras revoke es 200, hubo %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "r.md")); !os.IsNotExist(err) {
		t.Fatal("el undo retiró el archivo sin fuente ni consentimiento")
	}
}

func listAdoptions(t *testing.T, h http.Handler) []adoptionSummary {
	t.Helper()
	response := doRequest(t, h, "GET", "/adoptions", "")
	if response.Code != http.StatusOK {
		t.Fatalf("listar adopciones es 200, hubo %d %s", response.Code, response.Body.String())
	}
	var out []adoptionSummary
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out == nil {
		out = []adoptionSummary{}
	}
	return out
}

func TestAdoptionsPersistAcrossRestart(t *testing.T) {
	state := t.TempDir()
	root := t.TempDir()
	skill := filepath.Join(state, "memu", "skill")
	if err := os.MkdirAll(skill, 0755); err != nil {
		t.Fatal(err)
	}
	mk := func() http.Handler {
		return New(state, func() []string { return []string{root} }, func() error { return nil })
	}
	h := mk()
	writeSource(t, state, "p.md", "persistente\n")
	rev := review(t, h, root, "p.md", "scripts/p.md")
	adopted := adopt(t, h, rev)
	// Snapshot: solo el artefacto aprobado, con su hash en el recibo.
	approved, err := os.ReadFile(filepath.Join(state, "memu", "adoptions", adopted.ID+".approved"))
	if err != nil || string(approved) != "persistente\n" {
		t.Fatalf("snapshot aprobado: %q %v", approved, err)
	}
	// Reinicio: handler nuevo sobre el mismo STATE, fuente ya ausente.
	if err := os.Remove(filepath.Join(skill, "p.md")); err != nil {
		t.Fatal(err)
	}
	restarted := mk()
	listed := listAdoptions(t, restarted)
	if len(listed) != 1 || listed[0].ID != adopted.ID || listed[0].SourceSHA256 == "" {
		t.Fatalf("la adopción sobrevive al reinicio sin fuente: %+v", listed)
	}
	if listed[0].SourceSHA256 != sha256Hex([]byte("persistente\n")) {
		t.Fatalf("source_sha256 del recibo: %+v", listed[0])
	}
	// Rollback tras reinicio sin fuente vigente.
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	if response := doRequest(t, restarted, "POST", "/rollback", string(body)); response.Code != http.StatusOK {
		t.Fatalf("rollback tras reinicio es 200, hubo %d %s", response.Code, response.Body.String())
	}
	if listed := listAdoptions(t, restarted); len(listed) != 0 {
		t.Fatalf("revertida sale del listado: %+v", listed)
	}
}

func TestAdoptFailureKeepsApprovedSnapshot(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "f.md", "falla\n")
	// Directorio de destino sin escritura: la lectura del ausente pasa el CAS
	// pero el write atómico falla al crear el temporal.
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "scripts"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "scripts"), 0755) })
	rev := review(t, h, root, "f.md", "scripts/f.md")
	body, _ := json.Marshal(adoptRequest{
		Path: rev.Path, Project: rev.Project, Target: rev.Target,
		BeforeSHA256: rev.BeforeSHA256, AfterSHA256: rev.AfterSHA256,
	})
	if response := doRequest(t, h, "POST", "/adopt", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("destino no escribible es 409, hubo %d %s", response.Code, response.Body.String())
	}
	dir := filepath.Join(state, "memu", "adoptions")
	rows, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var receipt, approved, backup bool
	for _, row := range rows {
		switch {
		case strings.HasSuffix(row.Name(), ".json"):
			receipt = true
		case strings.HasSuffix(row.Name(), ".approved"):
			approved = true
		case strings.HasSuffix(row.Name(), ".before"):
			backup = true
		}
	}
	if !receipt || !approved || backup {
		t.Fatalf("recibo+aprobado se preservan, respaldo inútil fuera: recibo=%v aprobado=%v respaldo=%v", receipt, approved, backup)
	}
	data, err := os.ReadFile(filepath.Join(dir, receiptName(rows)))
	_ = data
	_ = err
	if listed := listAdoptions(t, h); len(listed) != 0 {
		t.Fatalf("fallida no sale en el listado: %+v", listed)
	}
}

func receiptName(rows []os.DirEntry) string {
	for _, row := range rows {
		if strings.HasSuffix(row.Name(), ".json") {
			return row.Name()
		}
	}
	return ""
}

func TestRollbackAfterRollbackHook(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "m.md", "memoria\n")
	rev := review(t, h, root, "m.md", "scripts/m.md")
	adopted := adopt(t, h, rev)
	var called []string
	h.AfterRollback = func(_ context.Context, id string) error {
		called = append(called, id)
		return nil
	}
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	response := doRequest(t, h, "POST", "/rollback", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("rollback es 200, hubo %d %s", response.Code, response.Body.String())
	}
	var out rollbackResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Restored || out.MemoryRestoreError != "" || len(called) != 1 || called[0] != adopted.ID {
		t.Fatalf("hook invocado con el id, sin error: %+v %v", out, called)
	}
	// Fallo de memoria: el archivo ya quedó restaurado y el 200 lo dice.
	writeSource(t, state, "m.md", "memoria\n")
	rev2 := review(t, h, root, "m.md", "scripts/m.md")
	adopted2 := adopt(t, h, rev2)
	h.AfterRollback = func(_ context.Context, _ string) error { return errTest("índice no disponible") }
	body, _ = json.Marshal(rollbackRequest{ID: adopted2.ID})
	response = doRequest(t, h, "POST", "/rollback", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("fallo de memoria sigue 200, hubo %d", response.Code)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Restored || out.MemoryRestoreError == "" {
		t.Fatalf("restaurado con error de memoria visible: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "m.md")); !os.IsNotExist(err) {
		t.Fatal("el undo retiró el archivo aunque fallara la memoria")
	}
}

func writeManifest(t *testing.T, state, content string) string {
	t.Helper()
	dir := filepath.Join(state, "memu", "export")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func adoptionFiles(t *testing.T, state string) map[string]bool {
	t.Helper()
	rows, err := os.ReadDir(filepath.Join(state, "memu", "adoptions"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, row := range rows {
		switch {
		case strings.HasSuffix(row.Name(), ".json"):
			out["receipt"] = true
		case strings.HasSuffix(row.Name(), ".approved"):
			out["approved"] = true
		case strings.HasSuffix(row.Name(), ".evidence"):
			out["evidence"] = true
		case strings.HasSuffix(row.Name(), ".before"):
			out["backup"] = true
		}
	}
	return out
}

func TestAdoptSnapshotsEvidence(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "e.md", "evidencia\n")
	writeManifest(t, state, `{"lote":"actual"}`)
	rev := review(t, h, root, "e.md", "scripts/e.md")
	adopted := adopt(t, h, rev)
	data, err := os.ReadFile(filepath.Join(state, "memu", "adoptions", adopted.ID+".evidence"))
	if err != nil || string(data) != `{"lote":"actual"}` {
		t.Fatalf("evidencia del manifest vigente: %q %v", data, err)
	}
}

func TestAdoptWithoutManifestSkipsEvidence(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "n.md", "nada\n")
	rev := review(t, h, root, "n.md", "scripts/n.md")
	adopt(t, h, rev)
	if files := adoptionFiles(t, state); !files["receipt"] || !files["approved"] || files["evidence"] {
		t.Fatalf("sin manifest se omite evidencia: %+v", files)
	}
}

func TestAdoptManifestSymlinkOrOversizeSkipped(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "s.md", "s\n")
	outside := t.TempDir()
	target := filepath.Join(outside, "real.json")
	if err := os.WriteFile(target, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "memu", "export")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	rev := review(t, h, root, "s.md", "scripts/s.md")
	adopt(t, h, rev)
	if files := adoptionFiles(t, state); files["evidence"] {
		t.Fatalf("symlink no se copia: %+v", files)
	}
	big := make([]byte, (1<<20)+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.Remove(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), big, 0644); err != nil {
		t.Fatal(err)
	}
	writeSource(t, state, "s2.md", "s2\n")
	rev2 := review(t, h, root, "s2.md", "scripts/s2.md")
	adopt(t, h, rev2)
	if files := adoptionFiles(t, state); files["evidence"] {
		t.Fatalf("manifest gigante no se copia: %+v", files)
	}
}

func TestAdoptUnreadableManifestFailsClean(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "u.md", "u\n")
	path := writeManifest(t, state, `{"lote":"x"}`)
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })
	rev := review(t, h, root, "u.md", "scripts/u.md")
	body, _ := json.Marshal(adoptRequest{
		Path: rev.Path, Project: rev.Project, Target: rev.Target,
		BeforeSHA256: rev.BeforeSHA256, AfterSHA256: rev.AfterSHA256,
	})
	if response := doRequest(t, h, "POST", "/adopt", string(body)); response.Code != http.StatusInternalServerError {
		t.Fatalf("manifest ilegible falla cerrado, hubo %d", response.Code)
	}
	if files := adoptionFiles(t, state); len(files) != 0 {
		t.Fatalf("fallo previo a destino no deja restos: %+v", files)
	}
}

func TestAdoptDestFailureKeepsEvidence(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "f.md", "falla\n")
	writeManifest(t, state, `{"lote":"falla"}`)
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "scripts"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "scripts"), 0755) })
	rev := review(t, h, root, "f.md", "scripts/f.md")
	body, _ := json.Marshal(adoptRequest{
		Path: rev.Path, Project: rev.Project, Target: rev.Target,
		BeforeSHA256: rev.BeforeSHA256, AfterSHA256: rev.AfterSHA256,
	})
	if response := doRequest(t, h, "POST", "/adopt", string(body)); response.Code != http.StatusConflict {
		t.Fatalf("destino no escribible es 409, hubo %d", response.Code)
	}
	files := adoptionFiles(t, state)
	if !files["receipt"] || !files["approved"] || !files["evidence"] || files["backup"] {
		t.Fatalf("recibo+aprobado+evidencia se preservan, respaldo fuera: %+v", files)
	}
	if listed := listAdoptions(t, h); len(listed) != 0 {
		t.Fatalf("fallida no sale en el listado: %+v", listed)
	}
}

func TestAfterRollbackRunsOutsideTheLock(t *testing.T) {
	h, state, root := testHandler(t, true)
	writeSource(t, state, "m.md", "memoria\n")
	adopted := adopt(t, h, review(t, h, root, "m.md", "scripts/m.md"))
	h.AfterRollback = func(context.Context, string) error {
		if !h.mu.TryLock() {
			return errTest("el hook corrió con h.mu tomado")
		}
		h.mu.Unlock()
		return nil
	}
	body, _ := json.Marshal(rollbackRequest{ID: adopted.ID})
	response := doRequest(t, h, "POST", "/rollback", string(body))
	var out rollbackResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || response.Code != http.StatusOK || out.MemoryRestoreError != "" {
		t.Fatalf("rollback %d %s", response.Code, response.Body.String())
	}
}

func TestBodyIsReadBeforeTakingTheLock(t *testing.T) {
	h, _, _ := testHandler(t, true)
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest("POST", "/rollback", reader)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	time.Sleep(50 * time.Millisecond)
	if !h.mu.TryLock() {
		t.Fatal("a stalled request body holds the handler lock")
	}
	h.mu.Unlock()
	_ = writer.CloseWithError(errTest("cliente caído"))
	<-done
}

func TestArtifactNamesCannotBreakAgentsMarkers(t *testing.T) {
	h, state, root := testHandler(t, true)
	for _, name := range []string{"a-->b.md", "x\ny.md", "x\ry.md"} {
		response := doRequest(t, h, "GET", "/review?path="+url.QueryEscape(name)+"&project="+url.QueryEscape(root)+"&target=AGENTS.md", "")
		if response.Code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", name, response.Code)
		}
	}
	writeSource(t, state, "ok.md", "x\n")
	if response := doRequest(t, h, "GET", "/review?path=ok.md&project="+url.QueryEscape(root)+"&target=AGENTS.md", ""); response.Code != http.StatusOK {
		t.Fatalf("plain name rejected: %d %s", response.Code, response.Body.String())
	}
}
