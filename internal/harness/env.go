package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// envMap converts an environment block into a lookup table.
func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	return values
}

// flattenEnv returns a stable, sorted environment block.
func flattenEnv(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env
}

// nativePath keeps inherited priority and exposes standard user installs, as
// agents.py did, without reading anything from the user's home itself.
func nativePath(env map[string]string) string {
	path := env["PATH"]
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	directories := strings.Split(path, string(os.PathListSeparator))
	home := env["HOME"]
	if home != "" && filepath.IsAbs(home) {
		for _, relative := range []string{".local/bin", ".nix-profile/bin"} {
			directory := filepath.Join(home, relative)
			if !containsString(directories, directory) {
				directories = append(directories, directory)
			}
		}
	}
	return strings.Join(directories, string(os.PathListSeparator))
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func absJoin(parts ...string) string {
	path, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		return filepath.Join(parts...)
	}
	return path
}

// lookPathIn resolves name in directory, mirroring shutil.which's
// single-directory form. An empty directory keeps the direct-name behaviour.
func lookPathIn(name, directory string) string {
	if name == "" {
		return ""
	}
	candidate := name
	if directory != "" && !strings.ContainsRune(name, os.PathSeparator) {
		candidate = filepath.Join(directory, name)
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return candidate
}

// lookPath resolves name over the full PATH block.
func lookPath(name, path string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return lookPathIn(name, "")
	}
	for _, directory := range strings.Split(path, string(os.PathListSeparator)) {
		if directory == "" {
			directory = "."
		}
		if found := lookPathIn(name, directory); found != "" {
			return found
		}
	}
	return ""
}

// usable rejects the private aliases that would replace their own source and
// keeps the discovered spelling of an executable regular file.
func usable(candidate string, forbidden map[string]bool) string {
	if candidate == "" {
		return ""
	}
	original, err := filepath.Abs(candidate)
	if err != nil {
		return ""
	}
	current := original
	visited := map[string]bool{}
	for !visited[current] {
		if forbidden[current] {
			return ""
		}
		visited[current] = true
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(current)
		if err != nil {
			return ""
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		current = filepath.Clean(target)
	}
	info, err := os.Stat(original)
	if err != nil || info.IsDir() {
		return ""
	}
	if err := unix.Access(original, unix.X_OK); err != nil {
		return ""
	}
	return original
}

// privateDir creates a directory only if it is not a symbolic link, as the
// legacy profile setup did.
func privateDir(path string) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return &os.PathError{Op: "reject symlink", Path: path, Err: unix.ELOOP}
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// privateFile writes content atomically with 0600. An existing non-regular
// target (symlink, directory or device) is refused instead of replaced.
func privateFile(path string, content []byte) error { return writePrivateFile(path, content, true) }

// privateVerifiedFile is privateFile without fsync, for content that is re-verified
// against pinned hashes (bundleComplete) and re-extracted when it does not match, so
// durability adds nothing.
func privateVerifiedFile(path string, content []byte) error {
	return writePrivateFile(path, content, false)
}

func writePrivateFile(path string, content []byte, durable bool) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return &os.PathError{Op: "reject non-regular file", Path: path, Err: unix.EPERM}
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".profile-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if durable {
		if err := temporary.Sync(); err != nil {
			temporary.Close()
			return err
		}
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
