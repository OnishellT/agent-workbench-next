package hosts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func ensureRemoteAgentDirs(fs *sftp.Client, target hostAgentTarget) error {
	parts := remotePathParts(target.stateDir)
	rootCount := len(remotePathParts(target.stateRoot))
	current := "/"
	for i, part := range parts {
		current = path.Join(current, part)
		info, err := fs.Lstat(current)
		if os.IsNotExist(err) {
			if err := fs.Mkdir(current); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = fs.Lstat(current)
			if err != nil {
				return err
			}
			if i >= rootCount {
				if err := fs.Chmod(current, 0o700); err != nil {
					return err
				}
				info, err = fs.Lstat(current)
				if err != nil {
					return err
				}
			}
		} else if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || ((i == rootCount-1 || i >= rootCount) && remoteFileUID(info) != target.uid) {
			return errHostAgentConflict
		}
		if i >= rootCount && info.Mode().Perm() != 0o700 {
			return errHostAgentConflict
		}
	}
	return nil
}

// checkRemoteAgentDirs rejects symlinks in every component, as host-agent's
// own EvalSymlinks check does, and distinguishes a missing install from unsafe state.
func checkRemoteAgentDirs(fs *sftp.Client, target hostAgentTarget, create bool) (bool, error) {
	if create {
		if err := ensureRemoteAgentDirs(fs, target); err != nil {
			return false, err
		}
		return true, nil
	}
	parts := remotePathParts(target.stateDir)
	rootCount := len(remotePathParts(target.stateRoot))
	current := "/"
	for i, part := range parts {
		current = path.Join(current, part)
		info, err := fs.Lstat(current)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || ((i == rootCount-1 || i >= rootCount) && remoteFileUID(info) != target.uid) {
			return false, errHostAgentConflict
		}
		if i >= rootCount && info.Mode().Perm() != 0o700 {
			return false, errHostAgentConflict
		}
	}
	return true, nil
}

func remotePathParts(value string) []string {
	trimmed := strings.Trim(value, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func createRemotePrivateDir(fs *sftp.Client, name string, uid int) error {
	if err := fs.Mkdir(name); err != nil {
		return err
	}
	if err := fs.Chmod(name, 0o700); err != nil {
		return err
	}
	return verifyRemoteDirectory(fs, name, uid, 0o700)
}

func verifyRemoteDirectory(fs *sftp.Client, name string, uid int, mode os.FileMode) error {
	info, err := fs.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != mode || remoteFileUID(info) != uid {
		return errHostAgentConflict
	}
	return nil
}

func verifyRemoteEntry(fs *sftp.Client, name string, mode os.FileMode, regular bool, uid int) error {
	info, err := fs.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || (regular && !info.Mode().IsRegular()) || info.Mode().Perm() != mode || remoteFileUID(info) != uid {
		return errHostAgentConflict
	}
	return nil
}

func remoteFileUID(info os.FileInfo) int {
	stat, ok := info.Sys().(*sftp.FileStat)
	if !ok || uint64(stat.UID) > uint64(^uint(0)>>1) {
		return -1
	}
	return int(stat.UID)
}

func readRemotePrivateFile(fs *sftp.Client, name string, uid int, mode os.FileMode, limit int64) ([]byte, os.FileInfo, error) {
	info, err := fs.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != mode || remoteFileUID(info) != uid || info.Size() < 0 || info.Size() > limit {
		return nil, nil, errHostAgentConflict
	}
	file, err := fs.Open(name)
	if err != nil {
		return nil, nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, nil, readErr
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	if int64(len(data)) > limit || int64(len(data)) != info.Size() {
		return nil, nil, errHostAgentConflict
	}
	return data, info, nil
}

func hashRemotePrivateFile(fs *sftp.Client, name string, uid int, mode os.FileMode) (string, os.FileInfo, error) {
	info, err := fs.Lstat(name)
	if err != nil {
		return "", nil, err
	}
	if !remotePrivateBinary(info, uid, mode) {
		return "", nil, errHostAgentConflict
	}
	file, err := fs.Open(name)
	if err != nil {
		return "", nil, err
	}
	hasher := sha256.New()
	n, readErr := io.Copy(hasher, io.LimitReader(file, hostAgentMaxBinary+1))
	closeErr := file.Close()
	if readErr != nil {
		return "", nil, readErr
	}
	if closeErr != nil {
		return "", nil, closeErr
	}
	if n != info.Size() || n > hostAgentMaxBinary {
		return "", nil, errHostAgentConflict
	}
	return hex.EncodeToString(hasher.Sum(nil)), info, nil
}

// remoteBinaryDigest hashes a private remote binary with the system sha256sum, so
// the executable itself is never streamed over SFTP on every inspect.
func remoteBinaryDigest(ctx context.Context, client *ssh.Client, fs *sftp.Client, name string, uid int, mode os.FileMode) (string, error) {
	before, err := fs.Lstat(name)
	if err != nil {
		return "", err
	}
	if !remotePrivateBinary(before, uid, mode) {
		return "", errHostAgentConflict
	}
	data, err := remoteCommand(ctx, client, "# workbench-owned-host-agent-digest\nLC_ALL=C /usr/bin/sha256sum < "+shellQuote(name))
	if err != nil {
		return "", err
	}
	digest, err := parseRemoteDigest(data)
	if err != nil {
		return "", err
	}
	after, err := fs.Lstat(name)
	if err != nil || !remotePrivateBinary(after, uid, mode) || after.Size() != before.Size() {
		return "", errHostAgentConflict
	}
	return digest, nil
}

func remotePrivateBinary(info os.FileInfo, uid int, mode os.FileMode) bool {
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Mode().Perm() == mode && remoteFileUID(info) == uid && info.Size() >= 1 && info.Size() <= hostAgentMaxBinary
}

// parseRemoteDigest accepts only the single stdin summary line "<64 hex>  -".
func parseRemoteDigest(data []byte) (string, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		return "", errHostAgentConflict
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 2 || fields[1] != "-" || !hostAgentHashPattern.MatchString(fields[0]) {
		return "", errHostAgentConflict
	}
	return fields[0], nil
}

func writeRemotePrivateFile(fs *sftp.Client, name string, data []byte, mode os.FileMode, nonce string, uid int) error {
	tmp := name + ".tmp-" + nonce
	file, err := fs.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = fs.Remove(tmp)
		return err
	}
	if err := verifyRemoteEntry(fs, tmp, mode, true, uid); err != nil {
		_ = fs.Remove(tmp)
		return err
	}
	if err := fs.Rename(tmp, name); err != nil {
		_ = fs.Remove(tmp)
		return err
	}
	return nil
}

func readRemoteHostAgentFile(fs *sftp.Client, name string, limit int64) ([]byte, error) {
	file, err := fs.Open(name)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > limit {
		return nil, errHostAgentConflict
	}
	return data, nil
}

func removeRemoteHostAgentFile(fs *sftp.Client, name string) error {
	if err := fs.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
