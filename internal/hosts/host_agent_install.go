package hosts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func openHostAgentBinary() (hostAgentBinary, error) {
	name, err := os.Executable()
	if err != nil {
		return hostAgentBinary{}, err
	}
	if !filepath.IsAbs(name) || filepath.Base(name) == "." || filepath.Base(name) == string(filepath.Separator) {
		return hostAgentBinary{}, errors.New("release binary path is invalid")
	}
	file, err := os.Open(name)
	if err != nil {
		return hostAgentBinary{}, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > hostAgentMaxBinary {
		file.Close()
		return hostAgentBinary{}, errors.New("release binary has invalid type or size")
	}
	hasher := sha256.New()
	n, err := io.Copy(hasher, io.LimitReader(file, hostAgentMaxBinary+1))
	if err != nil || n != info.Size() {
		file.Close()
		return hostAgentBinary{}, errors.New("release binary could not be hashed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return hostAgentBinary{}, err
	}
	return hostAgentBinary{file: file, size: info.Size(), hash: hex.EncodeToString(hasher.Sum(nil))}, nil
}

// hostAgentInstallTimeout sizes the install deadline from the release
// payloads: slow SSH links move ~85MB of workbench+magpie binaries, so the
// timeout scales with bytes instead of truncating an upload mid-copy. The
// floor covers handshake/launch/inspect on a healthy link; the ceiling bounds
// a dead one (the partial marker keeps the retry attributable either way).
func hostAgentInstallTimeout(totalBytes int64) time.Duration {
	const floor = 3 * time.Minute
	const ceiling = 30 * time.Minute
	if totalBytes < 0 {
		totalBytes = 0
	}
	timeout := floor + time.Duration(totalBytes/131072)*time.Second
	if timeout > ceiling {
		return ceiling
	}
	return timeout
}

// staleInstallLockAge bounds how long an install may legitimately hold the lock:
// it must exceed the largest install deadline (hostAgentInstallTimeout caps at
// 30m), or the reaper would delete the lock of a still-uploading attempt. An
// older lock can only belong to an attempt that died without cleanup (timeout,
// dropped connection, restarted Workbench). Only verified, stale locks are
// ever removed, and only inside the uid-verified per-host directory.
const staleInstallLockAge = 35 * time.Minute

// remoteInstallClock reads the remote clock for lock-age comparison. Both the
// timestamp and the lock mtime come from the same machine, so no local clock
// or skew can misjudge a live install as stale.
func remoteInstallClock(ctx context.Context, client *ssh.Client) (int64, error) {
	data, err := remoteCommand(ctx, client, "# workbench-owned-install-clock\nLC_ALL=C date +%s")
	if err != nil {
		return 0, err
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || epoch <= 0 {
		return 0, errors.New("reloj remoto ilegible para el bloqueo de instalación")
	}
	return epoch, nil
}

// clearStaleInstallLock removes an orphaned install lock so a failed attempt
// wedges neither status nor retry. It fails closed: any verification, clock,
// age, or removal doubt keeps the installing state and its 409. Hash/nonce
// ownership is untouched; only the lock file itself is removed.
func clearStaleInstallLock(ctx context.Context, client *ssh.Client, fs *sftp.Client, target hostAgentTarget, lockPath string, lockInfo os.FileInfo) bool {
	if err := verifyRemoteEntry(fs, lockPath, 0o600, true, target.uid); err != nil {
		return false
	}
	now, err := remoteInstallClock(ctx, client)
	if err != nil {
		return false
	}
	if now-lockInfo.ModTime().Unix() < int64(staleInstallLockAge/time.Second) {
		return false
	}
	if err := fs.Remove(lockPath); err != nil {
		return false
	}
	if _, err := fs.Lstat(lockPath); !os.IsNotExist(err) {
		return false
	}
	return true
}

// hostAgentPartialClaim attributes a partial first install to the transaction
// that staged it: the nonce binds the staging names (*.tmp-<nonce>), and the
// paths enumerate every name the attempt may leave behind.
type hostAgentPartialClaim struct {
	Nonce string   `json:"nonce"`
	Paths []string `json:"paths"`
}

// validPartialRecoveryPath accepts ONLY names a first install stages, relative
// to the verified per-host directory: the staging dir, the two binaries, the
// Magpie owner and the access token, plus any staging file bound to the claim
// nonce. The lock, the marker itself, the host-agent owner and the ready file
// are never deletable: listing one voids the whole claim.
func validPartialRecoveryPath(rel, nonce string) bool {
	if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	switch path.Base(rel) {
	case ".install.lock", hostAgentPartialFile, HostAgentOwnerFile, HostAgentReadyFile:
		return false
	}
	if strings.HasSuffix(path.Base(rel), ".tmp-"+nonce) {
		return true
	}
	switch rel {
	case "bin", "bin/" + hostAgentExecutable, "bin/magpie", NativeMagpieOwnerFile, HostAgentTokenFile:
		return true
	}
	return false
}

// removeClaimedPaths deletes marker-listed relative names that are present,
// uid-owned and transaction-shaped. It validates everything before deleting
// anything: one symlink, foreign uid, unexpected type or protected name aborts
// the whole recovery and nothing is removed.
func removeClaimedPaths(fs *sftp.Client, target hostAgentTarget, nonce string, rels []string) bool {
	type removal struct {
		full string
		dir  bool
	}
	var removals []removal
	for _, rel := range rels {
		if !validPartialRecoveryPath(rel, nonce) {
			return false
		}
		full := path.Join(target.stateDir, rel)
		info, err := fs.Lstat(full)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 || remoteFileUID(info) != target.uid {
			return false
		}
		isDir := info.IsDir()
		if isDir && rel != "bin" {
			return false
		}
		if !isDir && !info.Mode().IsRegular() {
			return false
		}
		removals = append(removals, removal{full, isDir})
	}
	ok := true
	for _, r := range removals {
		if r.dir {
			continue
		}
		if err := fs.Remove(r.full); err != nil {
			ok = false
		}
	}
	for _, r := range removals {
		if !r.dir {
			continue
		}
		// Only the now-empty staging dir goes; foreign content keeps it and
		// voids the marker removal below.
		if entries, err := fs.ReadDir(r.full); err != nil || len(entries) != 0 {
			ok = false
			continue
		}
		if err := fs.Remove(r.full); err != nil {
			ok = false
		}
	}
	return ok
}

// rollbackFirstInstall removes ONLY the names the failed first install may
// have staged, and only with its lock/nonce validated: a foreign or missing
// lock removes nothing, and the marker goes last so an interrupted rollback
// keeps attribution for the retry.
func rollbackFirstInstall(fs *sftp.Client, target hostAgentTarget, nonce string, created []string) {
	lock, _, err := readRemotePrivateFile(fs, path.Join(target.stateDir, ".install.lock"), target.uid, 0o600, 64)
	if err != nil || hex.EncodeToString(lock) != nonce {
		return
	}
	var rels []string
	for _, name := range created {
		rel, found := strings.CutPrefix(name, target.stateDir+"/")
		if !found || rel == hostAgentPartialFile {
			continue
		}
		rels = append(rels, rel)
	}
	if !removeClaimedPaths(fs, target, nonce, rels) {
		return
	}
	_ = fs.Remove(path.Join(target.stateDir, hostAgentPartialFile))
}

// recoverPartialFirstInstall heals the retry-409 of a timed-out first install:
// no lock, no owner, and a verified pre-upload marker lets the retry remove
// exactly that transaction's files and proceed. Any ambiguity (unmarked
// leftovers, hostile marker, foreign uid/symlink, live owner or lock) keeps
// the conflict: preexisting states are never deleted.
func recoverPartialFirstInstall(fs *sftp.Client, target hostAgentTarget) bool {
	if _, err := fs.Lstat(path.Join(target.stateDir, ".install.lock")); !os.IsNotExist(err) {
		return false
	}
	if _, err := fs.Lstat(path.Join(target.stateDir, HostAgentOwnerFile)); !os.IsNotExist(err) {
		return false
	}
	markerPath := path.Join(target.stateDir, hostAgentPartialFile)
	raw, _, err := readRemotePrivateFile(fs, markerPath, target.uid, 0o600, hostAgentMaxStateFile)
	if err != nil {
		return false
	}
	var claim hostAgentPartialClaim
	if err := decodeStrictJSON(bytes.NewReader(raw), &claim); err != nil {
		return false
	}
	if !hostAgentNoncePattern.MatchString(claim.Nonce) || len(claim.Paths) == 0 || len(claim.Paths) > 32 {
		return false
	}
	if !removeClaimedPaths(fs, target, claim.Nonce, claim.Paths) {
		return false
	}
	if err := fs.Remove(markerPath); err != nil {
		return false
	}
	return true
}

// singleNativeBinary accepts zero or one native Magpie binary.
func singleNativeBinary(binaries []hostAgentBinary) (*hostAgentBinary, error) {
	switch len(binaries) {
	case 0:
		return nil, nil
	case 1:
		return &binaries[0], nil
	}
	return nil, errors.New("native Magpie release binary is ambiguous")
}

func installRemoteHostAgent(ctx context.Context, client *ssh.Client, identity Identity, binary hostAgentBinary, nativeBinaries ...hostAgentBinary) (remoteHostAgent, bool, error) {
	nativeBinary, err := singleNativeBinary(nativeBinaries)
	if err != nil {
		return remoteHostAgent{}, false, err
	}
	// The install deadline owns the transport: when ctx ends (timeout or
	// cancel) the SSH client is closed, which also unblocks any SFTP write
	// that is mid-flight. SSHOperation closes it again on return (idempotent).
	defer context.AfterFunc(ctx, func() { _ = client.Close() })()
	target, err := discoverHostAgentTarget(ctx, client, identity.HostID)
	if err != nil {
		return remoteHostAgent{}, false, err
	}
	fs, err := sftp.NewClient(client)
	if err != nil {
		return remoteHostAgent{}, false, errors.New("SFTP no disponible para host-agent")
	}
	defer fs.Close()
	state, err := inspectOrRecoverHostAgent(ctx, client, fs, identity, target)
	if err != nil {
		return state, false, err
	}
	if state.status.State == "installing" {
		return state, false, errHostAgentInstalling
	}
	if state.status.Installed {
		return reconcileInstalledHostAgent(ctx, client, fs, identity, target, state, binary, nativeBinary)
	}
	return firstInstallHostAgent(ctx, client, fs, identity, target, state, binary, nativeBinary)
}

// inspectOrRecoverHostAgent inspects the remote state; a marker-attributed
// partial first install (timeout or dead transport mid-upload) is recovered
// and re-inspected, while anything ambiguous stays a conflict.
func inspectOrRecoverHostAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget) (remoteHostAgent, error) {
	state, err := inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
	if errors.Is(err, errHostAgentConflict) && recoverPartialFirstInstall(fs, target) {
		state, err = inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
	}
	return state, err
}

// reconcileInstalledHostAgent handles an owned install: verify the native
// Magpie, upgrade a different binary, or (re)start the helper when needed.
func reconcileInstalledHostAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget, state remoteHostAgent, binary hostAgentBinary, nativeBinary *hostAgentBinary) (remoteHostAgent, bool, error) {
	if nativeBinary != nil {
		if err := verifyRemoteNativeMagpie(ctx, client, fs, target, identity, nativeBinary.hash); err != nil {
			return state, false, err
		}
	}
	if state.owner.Hash != binary.hash {
		updated, err := upgradeRemoteHostAgent(ctx, client, fs, identity, target, state, binary)
		return updated, false, err
	}
	if state.status.Ready {
		_ = removeRemoteHostAgentUpgradeBackups(fs, target, state.owner.Nonce)
		return state, false, nil
	}
	if state.ready.PID > 0 {
		running, err := verifyOwnedHostAgentProcess(fs, target, identity, state.owner, state.ready)
		if err != nil {
			return state, false, err
		}
		if running {
			return state, false, nil
		}
	}
	if err := launchHostAgent(ctx, client, identity.HostID, target.stateDir); err != nil {
		return state, false, err
	}
	state, err := inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
	return state, false, err
}

// newInstallLock creates the exclusive install lock holding a fresh nonce and
// returns that nonce. An existing lock means another install is in progress.
func newInstallLock(fs *sftp.Client, lockPath string) (string, error) {
	nonceBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, nonceBytes); err != nil {
		return "", err
	}
	lockFile, err := fs.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		if os.IsExist(err) {
			return "", errHostAgentInstalling
		}
		return "", err
	}
	err = lockFile.Chmod(0o600)
	if err == nil {
		_, err = lockFile.Write(nonceBytes)
	}
	closeErr := lockFile.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = fs.Remove(lockPath)
		return "", err
	}
	return hex.EncodeToString(nonceBytes), nil
}

// partialInstallPaths lists every name a first install may stage; the retry
// only ever removes marker-listed names. "bin" comes first.
func partialInstallPaths(nonce string) []string {
	return []string{
		"bin",
		"bin/" + hostAgentExecutable,
		"bin/" + hostAgentExecutable + ".tmp-" + nonce,
		"bin/magpie",
		"bin/magpie.tmp-" + nonce,
		NativeMagpieOwnerFile,
		NativeMagpieOwnerFile + ".tmp-" + nonce,
		HostAgentTokenFile,
		HostAgentTokenFile + ".tmp-" + nonce,
		HostAgentOwnerFile + ".tmp-" + nonce,
	}
}

// writePartialClaim writes the pre-upload ownership marker while the transport
// is alive, creates bin/, and returns every path the rollback may need, marker
// first so an interrupted rollback keeps attribution.
func writePartialClaim(fs *sftp.Client, target hostAgentTarget, nonce string) (markerPath string, created []string, err error) {
	partialPaths := partialInstallPaths(nonce)
	claimData, err := json.Marshal(hostAgentPartialClaim{Nonce: nonce, Paths: partialPaths})
	if err != nil {
		return "", nil, err
	}
	markerPath = path.Join(target.stateDir, hostAgentPartialFile)
	if err := writeRemotePrivateFile(fs, markerPath, claimData, 0o600, nonce, target.uid); err != nil {
		return "", nil, err
	}
	if err := createRemotePrivateDir(fs, path.Join(target.stateDir, "bin"), target.uid); err != nil {
		return "", nil, err
	}
	created = []string{markerPath, path.Join(target.stateDir, "bin")}
	for _, rel := range partialPaths[1:] {
		created = append(created, path.Join(target.stateDir, rel))
	}
	return markerPath, created, nil
}

// stageHostAgentFiles uploads the binaries and writes the token and owner. The
// owner file is the commit point: it is written last.
func stageHostAgentFiles(ctx context.Context, fs *sftp.Client, identity Identity, target hostAgentTarget, binary hostAgentBinary, nativeBinary *hostAgentBinary, nonce string) error {
	binaryPath := path.Join(target.stateDir, "bin", hostAgentExecutable)
	if err := uploadHostAgentBinary(ctx, binary.file, binary.size, binary.hash, fs, binaryPath, nonce, target.uid); err != nil {
		return err
	}
	if nativeBinary != nil {
		if _, err := installRemoteNativeMagpie(ctx, fs, target, identity, *nativeBinary, nonce); err != nil {
			return err
		}
	}
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return err
	}
	token := []byte(base64.RawURLEncoding.EncodeToString(tokenBytes))
	if err := writeRemotePrivateFile(fs, path.Join(target.stateDir, HostAgentTokenFile), token, 0o600, nonce, target.uid); err != nil {
		return err
	}
	owner := HostAgentOwner{HostID: identity.HostID, UID: target.uid, Hash: binary.hash, Nonce: nonce, Executable: hostAgentExecutable}
	ownerData, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return writeRemotePrivateFile(fs, path.Join(target.stateDir, HostAgentOwnerFile), ownerData, 0o600, nonce, target.uid)
}

// firstInstallHostAgent installs into an empty per-host directory under the
// install lock, with a rollback that only touches marker-attributed names.
func firstInstallHostAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget, state remoteHostAgent, binary hostAgentBinary, nativeBinary *hostAgentBinary) (remoteHostAgent, bool, error) {
	if err := ensureRemoteAgentDirs(fs, target); err != nil {
		return state, false, err
	}
	entries, err := fs.ReadDir(target.stateDir)
	if err != nil {
		return state, false, err
	}
	if len(entries) != 0 {
		return state, false, errHostAgentConflict
	}
	lockPath := path.Join(target.stateDir, ".install.lock")
	nonce, err := newInstallLock(fs, lockPath)
	if err != nil {
		return state, false, err
	}
	// The cleanup owns the lock from here: every return below, including the
	// single-entry check, removes it instead of wedging status and retry.
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = fs.Remove(lockPath)
		}
	}()
	entries, err = fs.ReadDir(target.stateDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != ".install.lock" {
		return state, false, errHostAgentConflict
	}
	markerPath, createdPaths, err := writePartialClaim(fs, target, nonce)
	if err != nil {
		return state, false, err
	}
	launchAttempted := false
	defer func() {
		if !launchAttempted {
			rollbackFirstInstall(fs, target, nonce, createdPaths)
		}
	}()
	if err := stageHostAgentFiles(ctx, fs, identity, target, binary, nativeBinary, nonce); err != nil {
		return state, false, err
	}
	launchAttempted = true
	if err := launchHostAgent(ctx, client, identity.HostID, target.stateDir); err != nil {
		return state, true, err
	}
	if releaseInstallState(fs, identity.HostID, lockPath, markerPath) {
		lockHeld = false
	}
	state, err = inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
	if err != nil {
		return state, true, err
	}
	state.token = nil
	return state, true, nil
}

// releaseInstallState drops the install lock and the partial marker after a
// successful launch. The agent is running and owns its state, so a failure here
// must not report the install as failed: it is logged instead (a leftover lock
// is reaped as stale, a leftover marker is inert once the owner exists). It
// reports whether the lock is gone.
func releaseInstallState(fs *sftp.Client, hostID, lockPath, markerPath string) (lockReleased bool) {
	lockReleased = true
	if err := fs.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		lockReleased = false
		log.Printf("hosts: host-agent installed for %s but the install lock could not be removed: %v", hostID, err)
	}
	if err := fs.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		log.Printf("hosts: host-agent installed for %s but the partial marker could not be removed: %v", hostID, err)
	}
	return lockReleased
}

// ctxReader makes an io.Copy abort promptly once ctx ends, so the install
// deadline bounds the upload instead of only the steps around it.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func uploadHostAgentBinary(ctx context.Context, source *os.File, size int64, expectedHash string, fs *sftp.Client, name, nonce string, uid int) error {
	tmp := name + ".tmp-" + nonce
	remote, err := fs.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	if err := remote.Chmod(0o700); err != nil {
		remote.Close()
		_ = fs.Remove(tmp)
		return err
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		remote.Close()
		_ = fs.Remove(tmp)
		return err
	}
	hasher := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(remote, hasher), ctxReader{ctx, io.LimitReader(source, hostAgentMaxBinary+1)})
	closeErr := remote.Close()
	if copyErr != nil {
		_ = fs.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = fs.Remove(tmp)
		return closeErr
	}
	if n != size || n > hostAgentMaxBinary || hex.EncodeToString(hasher.Sum(nil)) != expectedHash {
		_ = fs.Remove(tmp)
		return errors.New("release binary cambió durante la instalación")
	}
	if err := verifyRemoteEntry(fs, tmp, 0o700, true, uid); err != nil {
		_ = fs.Remove(tmp)
		return err
	}
	remoteHash, _, err := hashRemotePrivateFile(fs, tmp, uid, 0o700)
	if err != nil || remoteHash != expectedHash {
		_ = fs.Remove(tmp)
		return errHostAgentConflict
	}
	if err := fs.Rename(tmp, name); err != nil {
		_ = fs.Remove(tmp)
		return err
	}
	return nil
}
