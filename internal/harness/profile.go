package harness

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
)

// The named OMP profiles keep the pinned upstream presets. Their bytes come
// from apps/omp (integration.lock.json) and are verified before use, because a
// profile that silently lost its preset would look like a valid empty profile.
//
//go:embed assets
var assets embed.FS

// The product's own OMP plugin trees ship inside the binary: no sidecar files
// beside the executable and no copy of any private user skill directory.
//
// The dot-prefixed plugin descriptors are part of the tree, so the embed
// pattern must include them.
//
//go:embed all:bundle
var bundle embed.FS

// bundlePins maps every bundled tree to its file digests. Its own digest names
// the extraction directory, so a new bundle never reuses an older extraction.
var bundlePins = sync.OnceValue(func() map[string]map[string]string {
	raw, err := bundle.ReadFile("bundle/pins.json")
	if err != nil {
		panic(err)
	}
	pins := map[string]map[string]string{}
	if err := json.Unmarshal(raw, &pins); err != nil {
		panic(err)
	}
	return pins
})

// bundleRevision identifies the embedded bundle contents.
func bundleRevision() string {
	raw, err := bundle.ReadFile("bundle/pins.json")
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// bundlePath extracts one bundled tree under the private state and returns its
// path. Every file is verified against the embedded manifest before it is used.
func (e *Engines) bundlePath(tree string) (string, error) {
	pins := bundlePins()[tree]
	if len(pins) == 0 {
		return "", fmt.Errorf("el recurso %s no está incluido en este artefacto", tree)
	}
	root := filepath.Join(e.state, "agents", "omp", "bundle", bundleRevision(), filepath.FromSlash(tree))
	if bundleComplete(root, pins) {
		return root, nil
	}
	for name, want := range pins {
		raw, err := bundle.ReadFile("bundle/" + tree + "/" + name)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			return "", fmt.Errorf("el recurso %s no coincide con el pin: %s", tree, name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := privateDir(filepath.Dir(target)); err != nil {
			return "", err
		}
		if err := privateVerifiedFile(target, raw); err != nil {
			return "", err
		}
	}
	return root, nil
}

// bundleComplete reports whether an existing extraction still matches the pins.
func bundleComplete(root string, pins map[string]string) bool {
	for name, want := range pins {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return false
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			return false
		}
	}
	return true
}

var presetHashes = map[string]string{
	"assets/personal/config.yml":        "8b186099ea0cb25ea1584314892042d76339cff0b830ce1c0ef6c10bec8a2a7f",
	"assets/personal/extensions/rtk.ts": "ace44e4a67ef9f1d5018fd4370060ec66b8a40fa41a9a2cf5fcc2f94d94d5419",
	"assets/work/config.yml":            "9a9c02f7ad5993c9348c6200cb818c0b9b1d76000c4fa47349716aa24fa26418",
}

var legacyPresetHashes = map[string]string{
	"personal": "7c8464f456eba1a08d6dd6b3e75570cedd7cf4cecb212c2dd581aa2b3aeda307",
	"work":     "eb645fa72bb837088df29c72d629a2cfee5cdfc7e730c8c35f23483270026968",
}

// ompAgentDir is the profile's own agent directory inside the private home.
func ompAgentDir(home, profile string) string {
	if profile == OMPProfileDefault {
		return filepath.Join(home, ".omp", "agent")
	}
	return filepath.Join(home, ".omp", "profiles", profile, "agent")
}

// prepareProfile creates the selected profile's private directories and its
// missing preset files, preserving whatever the user already has there. It
// never copies credentials between profiles: each profile owns its agent dir.
func (e *Engines) prepareProfile(home, profile, binary string, library bool, env []string) error {
	directory := filepath.Join(home, ".omp")
	agent := ompAgentDir(home, profile)
	paths := []string{directory, agent}
	if profile != OMPProfileDefault {
		paths = []string{directory, filepath.Join(directory, "profiles"), filepath.Dir(agent), agent}
	}
	for _, path := range paths {
		if err := privateDir(path); err != nil {
			return err
		}
	}
	presetDigest := ""
	if profile != OMPProfileDefault {
		config, err := bundled("assets/" + profile + "/config.yml")
		if err != nil {
			return err
		}
		sum := sha256.Sum256(config)
		presetDigest = hex.EncodeToString(sum[:])
		if err := writePreset(filepath.Join(agent, "config.yml"), config, legacyPresetHashes[profile]); err != nil {
			return err
		}
		extension, err := bundled("assets/personal/extensions/rtk.ts")
		if err != nil {
			return err
		}
		if err := writeIfMissing(filepath.Join(agent, "extensions", "rtk.ts"), extension); err != nil {
			return err
		}
	}
	desired := []string{}
	if profile != OMPProfileDefault {
		plugin, err := e.bundlePath("plugins/ponytail")
		if err != nil {
			return err
		}
		desired = append(desired, plugin)
	}
	if library {
		libraryPath, err := e.bundlePath("skill-libraries/dfm-project-kit")
		if err != nil {
			return err
		}
		desired = append(desired, libraryPath)
	}
	marker := filepath.Join(agent, ".workbench-profile.json")
	value, err := readMarker(marker)
	if err != nil {
		return err
	}
	libraries, _ := value["libraries"].([]any)
	if library && !containsAny(libraries, "dfm") {
		libraries = append(libraries, "dfm")
	}
	// Only the resources this module linked are managed: anything the user
	// added to the same profile is left untouched.
	previousPaths, _ := value["plugin_paths"].([]any)
	if !sameStrings(previousPaths, desired) {
		for _, path := range desired {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("el complemento OMP no está disponible: %w", err)
			}
			command := exec.Command(binary, "plugin", "link", path, "--json")
			command.Env = env
			if output, err := command.CombinedOutput(); err != nil {
				return fmt.Errorf("no se pudo enlazar el complemento OMP: %v: %s", err, truncate(string(output), 300))
			}
		}
	}
	updated := map[string]any{"schema": 1, "profile": profile, "libraries": libraries, "plugin_paths": desired}
	if profile != OMPProfileDefault {
		initial := value["initial_preset_sha256"]
		if initial == nil {
			initial = presetDigest
		}
		updated["initial_preset_sha256"] = initial
	}
	if !sameMarker(value, updated) {
		raw, err := json.MarshalIndent(updated, "", "  ")
		if err != nil {
			return err
		}
		return privateFile(marker, append(raw, '\n'))
	}
	return nil
}

func bundled(asset string) ([]byte, error) {
	got, err := assets.ReadFile(asset)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(got)
	if hex.EncodeToString(digest[:]) != presetHashes[asset] {
		return nil, fmt.Errorf("el recurso del perfil no coincide con el pin: %s", asset)
	}
	return got, nil
}

// writeIfMissing initializes one preset file only when it does not exist yet:
// an existing user file is preserved, and an unexpected state is reported
// instead of being silently replaced.
func writePreset(path string, content []byte, legacyHash string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return writeIfMissing(path, content)
	}
	if err != nil {
		return fmt.Errorf("no se pudo revisar el archivo de perfil %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("el archivo de perfil no es un archivo regular: %s", path)
	}
	if legacyHash == "" {
		return nil
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(old)
	if hex.EncodeToString(sum[:]) != legacyHash {
		return nil
	}
	return privateFile(path, content)
}

func writeIfMissing(path string, content []byte) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("el archivo de perfil no es un archivo regular: %s", path)
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
	default:
		return fmt.Errorf("no se pudo revisar el archivo de perfil %s: %w", path, err)
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return privateFile(path, content)
}

func readMarker(path string) (map[string]any, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return map[string]any{}, nil
	case err != nil:
		return nil, fmt.Errorf("no se pudo leer el marcador de perfil %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("el marcador de perfil no es un archivo regular: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el marcador de perfil %s: %w", path, err)
	}
	value := map[string]any{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("marcador de perfil inválido: %w", err)
	}
	return value, nil
}

func containsAny(values []any, want string) bool {
	for _, value := range values {
		if name, ok := value.(string); ok && name == want {
			return true
		}
	}
	return false
}

func sameStrings(values []any, want []string) bool {
	got := make([]string, 0, len(values))
	for _, value := range values {
		name, ok := value.(string)
		if !ok {
			return false
		}
		got = append(got, name)
	}
	if len(got) != len(want) {
		return false
	}
	sort.Strings(got)
	expected := append([]string(nil), want...)
	sort.Strings(expected)
	for i := range got {
		if got[i] != expected[i] {
			return false
		}
	}
	return true
}

func sameMarker(previous, updated map[string]any) bool {
	a, err := json.Marshal(previous)
	if err != nil {
		return false
	}
	b, err := json.Marshal(updated)
	if err != nil {
		return false
	}
	return string(a) == string(b)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
