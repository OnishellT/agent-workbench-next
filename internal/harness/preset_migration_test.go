package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePresetMigratesOnlyExactLegacyContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	legacy, current := []byte("legacy preset\n"), []byte("Magpie owns model routing\n")
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePreset(path, current, sha256Hex(legacy)); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(current) {
		t.Fatalf("exact legacy preset wasn't migrated: %q %v", got, err)
	}
	custom := []byte("user-modified preset\n")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePreset(path, current, sha256Hex(legacy)); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(custom) {
		t.Fatalf("user-modified preset was replaced: %q %v", got, err)
	}
}

func TestBundledOMPPresetsDoNotOwnProviderOrModelRoles(t *testing.T) {
	for _, profile := range []string{"personal", "work"} {
		config, err := bundled("assets/" + profile + "/config.yml")
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"providers:", "modelRoles:", "fallbackChains:"} {
			if strings.Contains(string(config), key) {
				t.Fatalf("%s preset still hardcodes Magpie-owned key %q", profile, key)
			}
		}
	}
}
