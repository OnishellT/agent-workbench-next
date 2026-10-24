// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// FO-09: el oráculo externo y sus controles explícitos deciden si el caso existe. Los
// recibos salen de las cuatro fases reales del trabajo en cola, no de una comprobación
// dentro del proceso. El control corre como el mismo usuario del producto, sin sandbox
// del sistema operativo: lo que se comprueba es el espacio de trabajo aislado, el hash del
// verificador congelado y el tiempo del comando.
const controlsVerify = `import sys
from pathlib import Path

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path.cwd()
value = (root / "value.txt").read_text().strip()
print(f"observed={value} <b>inerte</b>")
sys.exit(0 if value == "good" else 1)
`

const controlsRepair = `import sys
from pathlib import Path

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path.cwd()
(root / "value.txt").write_text("good\n")
`

const controlsInject = `import sys
from pathlib import Path

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path.cwd()
(root / "value.txt").write_text("bad\n")
`

// writeControlsFixture prepares a new project whose initial answer is wrong plus the
// external oracles: the valid one and the four invalid ones of the FO-09 contract.
func writeControlsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	writeControlsFile(t, filepath.Join(project, "value.txt"), "bad\n")
	writeControlsFile(t, filepath.Join(project, "sub", "notes.md"), "fixture: la respuesta inicial es incorrecta.\n")
	oracles := map[string]map[string]string{
		"oracle": {"verify.py": controlsVerify, "repair.py": controlsRepair, "inject.py": controlsInject},
		// Un verificador que siempre aprueba no puede acreditar nada.
		"negatives/green": {"verify.py": "raise SystemExit(0)\n", "repair.py": controlsRepair, "inject.py": controlsInject},
		// Un control negativo que no introduce el fallo deja pasar el control.
		"negatives/noop":        {"verify.py": controlsVerify, "repair.py": controlsRepair, "inject.py": "pass\n"},
		"negatives/setup_fail":  {"verify.py": controlsVerify, "repair.py": "import sys\nsys.exit(3)\n", "inject.py": controlsInject},
		"negatives/inject_fail": {"verify.py": controlsVerify, "repair.py": controlsRepair, "inject.py": "import sys\nsys.exit(4)\n"},
		// Un control que escribe en su propio verificador congelado rompe la integridad.
		"negatives/tamper": {"verify.py": controlsVerify, "repair.py": controlsRepair + `
Path(__file__).with_name("tampered.txt").write_text("mutated the frozen verifier\n")
`, "inject.py": controlsInject},
	}
	for name, files := range oracles {
		directory := filepath.Join(root, name)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for file, content := range files {
			writeControlsFile(t, filepath.Join(directory, file), content)
		}
	}
	return root
}

func writeControlsFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// dirFingerprint hashes names and bytes of a tree; it is the test's own check that the
// original oracle was not modified, independent from the module's hash function.
func dirFingerprint(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
	var names []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s\x00%s\x00", filepath.ToSlash(rel), data)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func queueControlsCase(t *testing.T, client *http.Client, baseURL, title, project, oracle string) string {
	t.Helper()
	status, body := callFoundryJSON(t, client, baseURL, http.MethodPost, "/api/foundry/cases", map[string]any{
		"title": title, "prompt": "Repara la respuesta inicial de value.txt.",
		"path": project, "verifier_path": oracle, "domain": "controls", "split": "origin",
		"verify":           []string{"{python}", "{verifier}/verify.py", "{workspace}"},
		"positive_control": []string{"{python}", "{verifier}/repair.py", "{workspace}"},
		"negative_control": []string{"{python}", "{verifier}/inject.py", "{workspace}"},
	})
	if status != http.StatusAccepted {
		t.Fatalf("encolar caso: %d %+v", status, body)
	}
	jobID, _ := body["job_id"].(string)
	if jobID == "" {
		t.Fatalf("el caso no devolvió job_id: %+v", body)
	}
	return jobID
}

func waitControlsJob(t *testing.T, client *http.Client, baseURL, jobID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		status, body := callFoundryJSON(t, client, baseURL, http.MethodGet, "/api/foundry/jobs/"+jobID, nil)
		if status != http.StatusOK {
			t.Fatalf("leer trabajo %s: %d %+v", jobID, status, body)
		}
		switch body["status"] {
		case "succeeded", "failed", "cancelled", "interrupted", "blocked":
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("el trabajo %s no terminó: %+v", jobID, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func controlsPhases(t *testing.T, result map[string]any) []string {
	t.Helper()
	snapshot, ok := result["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("el caso no publicó su manifest: %+v", result)
	}
	controls, ok := snapshot["controls"].([]any)
	if !ok {
		t.Fatalf("el manifest no publicó recibos de control: %+v", snapshot)
	}
	out := make([]string, 0, len(controls))
	for _, raw := range controls {
		control, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("recibo de control ilegible: %+v", raw)
		}
		out = append(out, fmt.Sprintf("%v:%v", control["phase"], control["status"]))
	}
	return out
}

func controlsCaseCount(t *testing.T, client *http.Client, baseURL string) int {
	t.Helper()
	status, body := callFoundryJSON(t, client, baseURL, http.MethodGet, "/api/foundry/cases", nil)
	if status != http.StatusOK {
		t.Fatalf("listar casos: %d %+v", status, body)
	}
	cases, ok := body["cases"].([]any)
	if !ok {
		t.Fatalf("listado de casos ilegible: %+v", body)
	}
	return len(cases)
}

func controlsSnapshots(t *testing.T, stateDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(stateDir, "foundry", "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}

func TestLearningCaseControlsAcceptRealOracleAndPreserveIt(t *testing.T) {
	root := writeControlsFixture(t)
	stateDir := filepath.Join(root, "state")
	_, server := newFoundryTestServer(t, stateDir, nil)
	client := server.Client()
	project := filepath.Join(root, "project")
	oracle := filepath.Join(root, "oracle")
	before := dirFingerprint(t, oracle)

	job := waitControlsJob(t, client, server.URL, queueControlsCase(t, client, server.URL, "valid case", project, oracle))
	if job["status"] != "succeeded" {
		t.Fatalf("el caso con oráculo válido falló: %+v", job)
	}
	result, _ := job["result"].(map[string]any)
	if result == nil {
		t.Fatalf("el trabajo no publicó resultado: %+v", job)
	}
	snapshot, _ := result["snapshot"].(map[string]any)
	if snapshot == nil {
		t.Fatalf("el caso no publicó su manifest: %+v", result)
	}
	// Las cuatro fases del contrato, con el negativo detectado de verdad.
	if got, want := strings.Join(controlsPhases(t, result), ","), "positive_setup:pass,positive_verify:pass,negative_setup:pass,negative_verify:fail"; got != want {
		t.Fatalf("fases de control = %q, se esperaba %q", got, want)
	}
	// La salida real de cada verificación viaja en el recibo, no sólo el código de salida.
	snapshotControls, _ := snapshot["controls"].([]any)
	outputs := map[string]string{}
	for _, raw := range snapshotControls {
		control, _ := raw.(map[string]any)
		phase, _ := control["phase"].(string)
		outputs[phase], _ = control["stdout"].(string)
	}
	// La salida se conserva literal, incluso si parece HTML: es un dato, no una página.
	if !strings.Contains(outputs["positive_verify"], "observed=good") || !strings.Contains(outputs["negative_verify"], "observed=bad") {
		t.Fatalf("salidas de verificación = %+v", outputs)
	}
	for _, phase := range []string{"positive_verify", "negative_verify"} {
		if !strings.Contains(outputs[phase], "<b>inerte</b>") {
			t.Fatalf("la salida de %s no quedó literal: %q", phase, outputs[phase])
		}
	}
	// Los marcadores se conservan congelados: el manifest no filtra la ruta externa.
	for field, want := range map[string][]string{
		"verify":           {"{python}", "{verifier}/verify.py", "{workspace}"},
		"positive_control": {"{python}", "{verifier}/repair.py", "{workspace}"},
		"negative_control": {"{python}", "{verifier}/inject.py", "{workspace}"},
	} {
		got, _ := json.Marshal(result[field])
		expected, _ := json.Marshal(want)
		if string(got) != string(expected) {
			t.Fatalf("%s congelado = %s, se esperaba %s", field, got, expected)
		}
	}
	snapshot, _ = result["snapshot"].(map[string]any)
	if snapshot["git_commit"] != nil {
		t.Fatalf("sin ref explícita el caso no debe acreditar commit: %+v", snapshot["git_commit"])
	}
	for _, field := range []string{"project_hash", "verifier_hash"} {
		value, _ := snapshot[field].(string)
		if len(value) != 64 {
			t.Fatalf("%s = %q, se esperaba un hash sha256", field, value)
		}
	}
	// El control positivo reparó sólo la copia aislada: el proyecto y el oráculo
	// originales quedan intactos.
	frozen := filepath.Join(stateDir, "foundry", snapshot["directory"].(string))
	data, err := os.ReadFile(filepath.Join(frozen, "project", "value.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bad\n" {
		t.Fatalf("el snapshot no conserva la respuesta inicial: %q", data)
	}
	if data, err := os.ReadFile(filepath.Join(project, "value.txt")); err != nil || string(data) != "bad\n" {
		t.Fatalf("el control modificó el proyecto original: %q (%v)", data, err)
	}
	if after := dirFingerprint(t, oracle); after != before {
		t.Fatalf("el oráculo externo cambió: %s → %s", before, after)
	}
	// El caso queda utilizable por el siguiente gate: controles acreditados en el listado.
	_, body := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/cases", nil)
	cases, _ := body["cases"].([]any)
	if len(cases) != 1 {
		t.Fatalf("casos publicados = %d, se esperaba 1", len(cases))
	}
	listed, _ := cases[0].(map[string]any)
	if listed["controls_verified"] != true || listed["positive_control_status"] != "pass" || listed["negative_control_status"] != "fail" {
		t.Fatalf("el caso no acredita sus controles: %+v", listed)
	}
	if listed["title"] != "valid case" {
		t.Fatalf("el listado no publica el caso creado: %+v", listed)
	}
}

func TestLearningCaseControlsRefuseInvalidOraclesWithoutTrace(t *testing.T) {
	root := writeControlsFixture(t)
	stateDir := filepath.Join(root, "state")
	_, server := newFoundryTestServer(t, stateDir, nil)
	client := server.Client()
	project := filepath.Join(root, "project")
	oracle := filepath.Join(root, "oracle")

	valid := waitControlsJob(t, client, server.URL, queueControlsCase(t, client, server.URL, "valid case", project, oracle))
	if valid["status"] != "succeeded" {
		t.Fatalf("el caso válido previo falló: %+v", valid)
	}
	validResult, _ := valid["result"].(map[string]any)
	validSnapshot, _ := validResult["snapshot"].(map[string]any)
	validHash, _ := validSnapshot["verifier_hash"].(string)
	kept := controlsSnapshots(t, stateDir)

	cases := []struct {
		name     string
		expected string
	}{
		{"green", "no detectó el defecto del control negativo"},
		{"noop", "no detectó el defecto del control negativo"},
		{"setup_fail", "El control positivo no se pudo preparar"},
		{"inject_fail", "El control negativo debe preparar el defecto"},
		{"tamper", "modificó el verificador congelado"},
	}
	for _, test := range cases {
		job := waitControlsJob(t, client, server.URL, queueControlsCase(
			t, client, server.URL, "control "+test.name, project, filepath.Join(root, "negatives", test.name)))
		if job["status"] != "failed" {
			t.Fatalf("%s: el control inválido no falló: %+v", test.name, job)
		}
		message, _ := job["error"].(string)
		if !strings.Contains(message, test.expected) {
			t.Fatalf("%s: error = %q, se esperaba %q", test.name, message, test.expected)
		}
		if count := controlsCaseCount(t, client, server.URL); count != 1 {
			t.Fatalf("%s: casos = %d, se esperaba que no apareciera ninguno", test.name, count)
		}
		if snapshots := controlsSnapshots(t, stateDir); strings.Join(snapshots, ",") != strings.Join(kept, ",") {
			t.Fatalf("%s: snapshots = %v, se esperaba %v sin restos parciales", test.name, snapshots, kept)
		}
		if _, err := os.Stat(filepath.Join(stateDir, "foundry", validSnapshot["directory"].(string), "verifier")); err != nil {
			t.Fatalf("%s: el caso válido previo se perdió: %v", test.name, err)
		}
		_, body := callFoundryJSON(t, client, server.URL, http.MethodGet, "/api/foundry/cases", nil)
		listed, _ := body["cases"].([]any)
		first, _ := listed[0].(map[string]any)
		snapshot, _ := first["snapshot"].(map[string]any)
		if snapshot["verifier_hash"] != validHash {
			t.Fatalf("%s: el caso válido previo cambió: %+v", test.name, snapshot)
		}
	}
	// Recuperación: los mismos controles válidos vuelven a producir un caso real.
	recovered := waitControlsJob(t, client, server.URL, queueControlsCase(t, client, server.URL, "recovery case", project, oracle))
	if recovered["status"] != "succeeded" {
		t.Fatalf("la recuperación con controles válidos falló: %+v", recovered)
	}
	if count := controlsCaseCount(t, client, server.URL); count != 2 {
		t.Fatalf("casos tras la recuperación = %d, se esperaban 2", count)
	}
	if snapshots := controlsSnapshots(t, stateDir); len(snapshots) != 2 {
		t.Fatalf("snapshots tras la recuperación = %v", snapshots)
	}
}
