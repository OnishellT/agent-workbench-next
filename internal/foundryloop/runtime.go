package foundryloop

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// extractRuntime unpacks the embedded Foundry package under root/<content hash>/ and
// returns that directory, which holds foundry_loop/. The directory name is the hash of
// the content, so a new Workbench build never reuses stale files and an existing,
// complete extraction is reused as is. The final directory appears atomically (rename
// of a finished temporary tree); concurrent or interrupted extractions leave no
// half-written runtime behind.
func extractRuntime(tree fs.FS, root string) (string, error) {
	files, digest, err := listRuntime(tree)
	if err != nil {
		return "", err
	}
	final := filepath.Join(root, digest)
	if info, err := os.Stat(filepath.Join(final, ".complete")); err == nil && info.Mode().IsRegular() {
		return final, nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(root, ".extract-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	for _, name := range files {
		data, err := fs.ReadFile(tree, name)
		if err != nil {
			return "", err
		}
		target := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(staging, ".complete"), []byte(digest), 0o600); err != nil {
		return "", err
	}
	// A previous interrupted run may have left an incomplete directory of this name.
	if err := os.RemoveAll(final); err != nil {
		return "", err
	}
	if err := os.Rename(staging, final); err != nil {
		// Another process finished the same extraction first.
		if _, statErr := os.Stat(filepath.Join(final, ".complete")); statErr == nil {
			return final, nil
		}
		return "", err
	}
	return final, nil
}

// listRuntime returns the regular files to extract (sorted, slash separated) and a
// digest over their names and content. Bytecode caches are never part of the runtime.
func listRuntime(tree fs.FS) ([]string, string, error) {
	var files []string
	err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path.Base(name) == "__pycache__" {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasSuffix(name, ".pyc") {
			return nil
		}
		files = append(files, name)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", errors.New("embedded Foundry runtime is empty")
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, name := range files {
		data, err := fs.ReadFile(tree, name)
		if err != nil {
			return nil, "", err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data))
		hash.Write(data)
	}
	return files, "v1-" + hex.EncodeToString(hash.Sum(nil))[:16], nil
}
