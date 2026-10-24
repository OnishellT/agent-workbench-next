package gui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/agentenv"
	"github.com/yetone/magpie/internal/backup"
	"github.com/yetone/magpie/internal/library"
)

// folderOnly is the app's windows as far as an export needs them.
type folderOnly struct{ Windows }

func (folderOnly) OpenFolder(string) error { return nil }

// sandboxHome gives the test a machine of its own: no agents, no magpie files.
func sandboxHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	t.Setenv("PATH", "")
	for _, v := range agentenv.Vars {
		t.Setenv(v, "")
	}
	t.Setenv("APPDATA", "")
	t.Setenv("LOCALAPPDATA", "")
	return h
}

// The Settings page's Export and Import carry the library, and leave it
// when its box is unticked.
func TestBackupLibraryRoundTrip(t *testing.T) {
	h := sandboxHome(t)
	text := "Be brief."
	if _, err := library.SaveInstructions(library.InstructionsChange{Shared: &text}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	backupRoutes(mux, folderOnly{})
	post := func(path string, body any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(string(b))))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	export := func(lib bool) []byte {
		t.Helper()
		out := post("/api/backup/export", map[string]any{"pass": "pw", "keys": true, "library": lib})
		if out["library"] != lib {
			t.Fatalf("export: %v", out)
		}
		p, _ := out["path"].(string)
		data, err := os.ReadFile(filepath.Join(h, strings.TrimPrefix(p, "~")))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	with, without := export(true), export(false)

	sandboxHome(t) // another machine
	enc := base64.StdEncoding.EncodeToString
	if out := post("/api/backup/import", map[string]any{"data": enc(without), "pass": "pw", "agents": true, "library": true}); out["Library"] != false {
		t.Fatalf("a file without it: %v", out)
	}
	if out := post("/api/backup/import", map[string]any{"data": enc(with), "pass": "pw", "agents": true, "library": false}); out["Library"] != false {
		t.Fatalf("unticked: %v", out)
	}
	if iv, _ := library.ReadInstructions(); iv.Shared != "" {
		t.Fatalf("the library came in unticked: %q", iv.Shared)
	}
	if out := post("/api/backup/import", map[string]any{"data": enc(with), "pass": "pw", "agents": true, "library": true}); out["Library"] != true {
		t.Fatalf("import: %v", out)
	}
	if iv, _ := library.ReadInstructions(); iv.Shared != "Be brief." {
		t.Fatalf("instructions: %q", iv.Shared)
	}
}

// exportPosts runs one export through the real route and hands back its answer.
func exportPosts(t *testing.T, w Windows, body map[string]any) (map[string]any, string) {
	t.Helper()
	h := sandboxHome(t)
	mux := http.NewServeMux()
	backupRoutes(mux, w)
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/backup/export", strings.NewReader(string(b))))
	if rec.Code != 200 {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, h
}

// The page served to a browser has no computer of its own to save into: the
// export writes nothing here and hands the sealed file back for the browser
// to save, which is what a reader of magpie web on a server has.
func TestBackupExportWebHandsTheFileToTheBrowser(t *testing.T) {
	out, home := exportPosts(t, webHost{}, map[string]any{"pass": "pw", "keys": true, "library": true})
	if path, ok := out["path"]; ok {
		t.Fatalf("a web export named a path on this computer: %v", path)
	}
	name, _ := out["name"].(string)
	if !strings.HasPrefix(name, "magpie-") || !strings.HasSuffix(name, backup.Ext) {
		t.Fatalf("the answer names the file %q", name)
	}
	enc, _ := out["data"].(string)
	data, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(data) == 0 {
		t.Fatalf("the answer carries no sealed file: %d bytes, %v", len(data), err)
	}
	// what it carries is the sealed bundle itself: it opens with the
	// passphrase and not with another
	if _, err := backup.Open(data, "another"); err == nil {
		t.Fatal("another passphrase opened the export")
	}
	if _, err := backup.Open(data, "pw"); err != nil {
		t.Fatalf("the export doesn't open with its passphrase: %v", err)
	}
	// and nothing was written on this computer
	err = filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), backup.Ext) {
			t.Errorf("the web export wrote %s on this computer", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The app's own windows keep the export they had: the file in Downloads,
// whole and private, its path in the answer.
func TestBackupExportOnTheDesktopWritesTheFile(t *testing.T) {
	out, home := exportPosts(t, folderOnly{}, map[string]any{"pass": "pw", "keys": true, "library": true})
	path, _ := out["path"].(string)
	if !strings.HasPrefix(path, "~") {
		t.Fatalf("the answer names the path %q", path)
	}
	name := filepath.Join(home, strings.TrimPrefix(path, "~"))
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("the export is %v, not 0600", perm)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Open(data, "pw"); err != nil {
		t.Fatalf("the file on disk doesn't open with its passphrase: %v", err)
	}
	if _, ok := out["data"]; ok {
		t.Fatal("a desktop export sent the file to the page as well")
	}
}
