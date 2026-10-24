package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOpenCreatesPrivateEmptySettingsAndRoundTrips(t *testing.T) {
	dir := privateTempDir(t)
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot(); got.Capture || len(got.Projects) != 0 {
		t.Fatalf("initial settings = %#v", got)
	}
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("settings mode = %o, want 600", info.Mode().Perm())
	}
	want := store.Snapshot()
	want.Capture = true
	want.Projects = []Project{{Path: t.TempDir(), Name: "Proyecto λ"}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot(); got.Capture != want.Capture || len(got.Projects) != 1 || got.Projects[0] != want.Projects[0] {
		t.Fatalf("reopened settings = %#v, want %#v", got, want)
	}
}

func TestOpenRejectsSymlinksAndInvalidPrivateState(t *testing.T) {
	t.Run("settings symlink", func(t *testing.T) {
		dir := privateTempDir(t)
		target := filepath.Join(t.TempDir(), "target")
		data := []byte("{\"capture\":false,\"projects\":[]}")
		if err := os.WriteFile(target, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, fileName)); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("Open symlink error = %v", err)
		}
	})
	t.Run("state directory symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "state-link")
		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(link); err == nil {
			t.Fatal("Open accepted a symlink state directory")
		}
	})
	t.Run("nonprivate state directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil {
			t.Fatal("Open accepted nonprivate state directory")
		}
	})
	t.Run("world readable settings file", func(t *testing.T) {
		dir := privateTempDir(t)
		path := filepath.Join(dir, fileName)
		data := []byte("{\"capture\":false,\"projects\":[]}")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("Open permissions error = %v", err)
		}
	})
}

func TestOpenRejectsInvalidSettingsJSON(t *testing.T) {
	cases := []struct{ name, data string }{
		{"malformed", "{"},
		{"missing field", "{\"capture\":false}"},
		{"unknown field", "{\"capture\":false,\"projects\":[],\"extra\":true}"},
		{"wrong legacy Orbit type", "{\"capture\":false,\"projects\":[],\"orbit_local_worker\":\"false\"}"},
		{"wrong type", "{\"capture\":\"false\",\"projects\":[]}"},
		{"null projects", "{\"capture\":false,\"projects\":null}"},
		{"trailing value", "{\"capture\":false,\"projects\":[]} {}"},
		{"duplicate key", "{\"capture\":false,\"capture\":true,\"projects\":[]}"},
		{"invalid project name type", "{\"capture\":false,\"projects\":[{\"path\":\"/\",\"name\":null}]}"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := privateTempDir(t)
			if err := os.WriteFile(filepath.Join(dir, fileName), []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); err == nil {
				t.Fatalf("Open accepted invalid settings %s", test.data)
			}
		})
	}
}

func TestValidateRequiresCanonicalProjectState(t *testing.T) {
	dir := t.TempDir()
	values := Defaults()
	values.Projects = []Project{{Path: dir, Name: strings.Repeat("x", maxNameRunes)}}
	if err := Validate(values); err != nil {
		t.Fatalf("valid boundary rejected: %v", err)
	}
	values.Projects[0].Name += "x"
	if err := Validate(values); err == nil {
		t.Fatal("accepted name longer than 120 characters")
	}
	values = Defaults()
	values.Projects = []Project{{Path: "relative", Name: "relative"}}
	if err := Validate(values); err == nil {
		t.Fatal("accepted relative path")
	}
	values.Projects = []Project{{Path: filepath.Join(dir, "missing"), Name: "missing"}}
	if err := Validate(values); err == nil {
		t.Fatal("accepted missing directory")
	}
	values.Projects = []Project{{Path: dir + string(os.PathSeparator) + "..", Name: "unclean"}}
	if err := Validate(values); err == nil {
		t.Fatal("accepted noncanonical path")
	}
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	values.Projects = []Project{{Path: link, Name: "symlink"}}
	if err := Validate(values); err == nil {
		t.Fatal("strict validation accepted symlink project")
	}
	values.Projects = make([]Project, maxProjects+1)
	if err := Validate(values); err == nil {
		t.Fatal("accepted more than 100 projects")
	}
}

func TestNormalizeAndSaveCanonicalizeProjectAndLegacyName(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	values := Defaults()
	values.Projects = []Project{{Path: link}}
	normalized, err := Normalize(values)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Projects[0].Path != target || normalized.Projects[0].Name != filepath.Base(target) {
		t.Fatalf("normalized project = %#v, target %q", normalized.Projects[0], target)
	}
	store, err := Open(privateTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(values); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Projects[0]; got != normalized.Projects[0] {
		t.Fatalf("Save did not canonicalize: %#v", got)
	}
	if _, err := Open(store.stateDir); err != nil {
		t.Fatalf("saved state is not strictly canonical: %v", err)
	}

	values.Projects = []Project{{Path: target, Name: strings.Repeat("λ", maxNameRunes+1)}}
	if err := store.Save(values); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Projects[0].Name
	if utf8.RuneCountInString(got) != maxNameRunes || got != strings.Repeat("λ", maxNameRunes) {
		t.Fatalf("name truncation = %d runes %q", utf8.RuneCountInString(got), got)
	}
}

func TestNormalizeRejectsBrokenProjectSymlinkAndControls(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken")
	if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), broken); err != nil {
		t.Fatal(err)
	}
	for _, project := range []Project{
		{Path: broken, Name: "broken"},
		{Path: t.TempDir() + "\n", Name: "control in path"},
		{Path: t.TempDir(), Name: "bad\x00name"},
	} {
		values := Defaults()
		values.Projects = []Project{project}
		if _, err := Normalize(values); err == nil {
			t.Errorf("Normalize accepted invalid project %#v", project)
		}
	}
}

func TestSaveValidationFailurePreservesPreviousState(t *testing.T) {
	store, err := Open(privateTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	values := store.Snapshot()
	values.Capture = true
	if err := store.Save(values); err != nil {
		t.Fatal(err)
	}
	bad := store.Snapshot()
	bad.Projects = []Project{{Path: "relative", Name: "invalid"}}
	if err := store.Save(bad); err == nil {
		t.Fatal("Save accepted an invalid project")
	}
	if got := store.Snapshot(); !got.Capture || len(got.Projects) != 0 {
		t.Fatalf("failed save changed settings: %#v", got)
	}
}

func TestProjectJSONRejectsUnknownAndDuplicateFields(t *testing.T) {
	for _, input := range []string{
		"{\"path\":\"/\",\"name\":\"x\",\"extra\":1}",
		"{\"path\":\"/\",\"path\":\"/tmp\"}",
	} {
		var project Project
		if err := json.Unmarshal([]byte(input), &project); err == nil {
			t.Errorf("accepted invalid project JSON %s", input)
		}
	}
}

// Settings written before Orbit was retired still carry its two keys. They load
// without losing projects, the retired values are discarded, and the next save
// writes the file without them.
func TestOpenReadsRetiredOrbitSettingsWithoutLosingProjects(t *testing.T) {
	dir := privateTempDir(t)
	project := t.TempDir()
	data := []byte("{\"capture\":false,\"projects\":[{\"path\":\"" + project + "\",\"name\":\"Kept\"}],\"orbit_local_worker\":true,\"orbit_image\":\"image:latest\"}")
	if err := os.WriteFile(filepath.Join(dir, fileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("legacy Orbit settings blocked startup: %v", err)
	}
	values := store.Snapshot()
	if len(values.Projects) != 1 || values.Projects[0].Path != project || values.Projects[0].Name != "Kept" {
		t.Fatalf("legacy Orbit settings lost projects: %#v", values)
	}
	if err := store.Save(values); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "orbit") {
		t.Fatalf("retired Orbit settings were persisted again: %s", saved)
	}
	var wire map[string]any
	if err := json.Unmarshal(saved, &wire); err != nil {
		t.Fatal(err)
	}
	projects, ok := wire["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("projects missing after rewrite: %s", saved)
	}
}

func TestStoreRejectsRootHomeAndStateDirectoryProjects(t *testing.T) {
	root := privateTempDir(t)
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(state, "models")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(root, "project")
	if err := os.Mkdir(ok, 0700); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"root": "/", "home": home, "state": state, "inside": inside, "ancestor": root} {
		if _, err := store.Normalize(Values{Projects: []Project{{Path: path}}}); err == nil {
			t.Errorf("%s: accepted %q", name, path)
		}
		if err := store.Save(Values{Projects: []Project{{Path: path}}}); err == nil {
			t.Errorf("%s: Save accepted %q", name, path)
		}
	}
	if _, err := store.Normalize(Values{Projects: []Project{{Path: ok}}}); err != nil {
		t.Fatalf("sibling project rejected: %v", err)
	}
	if _, err := Normalize(Values{Projects: []Project{{Path: "/"}}}); err == nil {
		t.Fatal("package Normalize accepted /")
	}
}
