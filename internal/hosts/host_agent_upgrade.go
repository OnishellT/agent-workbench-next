package hosts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func upgradeRemoteHostAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget, state remoteHostAgent, binary hostAgentBinary) (remoteHostAgent, error) {
	if state.ready.PID < 1 {
		return state, fmt.Errorf("%w: no existe un recibo de proceso verificable", errHostAgentConflict)
	}
	previous := state
	lockPath := path.Join(target.stateDir, ".install.lock")
	nonce, err := newInstallLock(fs, lockPath)
	if err != nil {
		return state, err
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = fs.Remove(lockPath)
		}
	}()
	if err := verifyRemoteHostAgentForUpgrade(fs, target, identity, state); err != nil {
		return state, err
	}
	running, err := verifyOwnedHostAgentProcess(fs, target, identity, state.owner, state.ready)
	if err != nil {
		return state, err
	}
	binaryPath := path.Join(target.stateDir, "bin", hostAgentExecutable)
	ownerPath := path.Join(target.stateDir, HostAgentOwnerFile)
	binaryStage := binaryPath + ".upgrade-" + nonce
	ownerStage := ownerPath + ".upgrade-" + nonce
	defer func() {
		_ = removeRemoteHostAgentFile(fs, binaryStage)
		_ = removeRemoteHostAgentFile(fs, ownerStage)
	}()
	if err := stageHostAgentUpgrade(ctx, fs, target, state, binary, nonce, binaryStage, ownerStage); err != nil {
		return state, err
	}
	if running {
		if err := stopOwnedHostAgentProcess(ctx, client, identity, target, state.owner, state.ready.PID); err != nil {
			return state, err
		}
	}
	if err := removeRemoteHostAgentFile(fs, path.Join(target.stateDir, HostAgentReadyFile)); err != nil {
		if running {
			_ = launchHostAgent(ctx, client, identity.HostID, target.stateDir)
		}
		return state, err
	}
	binaryBackup := binaryPath + ".previous-" + nonce
	ownerBackup := ownerPath + ".previous-" + nonce
	if err := replaceRemoteHostAgentFiles(fs, binaryPath, ownerPath, binaryStage, ownerStage, binaryBackup, ownerBackup); err != nil {
		if running {
			_ = launchHostAgent(ctx, client, identity.HostID, target.stateDir)
		}
		return state, err
	}
	if err := fs.Remove(lockPath); err != nil {
		return state, err
	}
	lockHeld = false
	if err := launchHostAgent(ctx, client, identity.HostID, target.stateDir); err != nil {
		return state, err
	}
	state, err = awaitUpgradedHostAgent(ctx, client, fs, identity, target, binary, nonce)
	if err != nil {
		return state, err
	}
	if !state.status.Ready && state.ready.PID > 0 {
		return rollbackUpgradedHostAgent(ctx, client, fs, identity, target, state, previous, nonce)
	}
	if state.status.Ready {
		_ = removeRemoteHostAgentUpgradeBackups(fs, target, nonce)
	}
	state.token = nil
	return state, nil
}

// stageHostAgentUpgrade uploads the new binary and writes the new owner next to
// the live files; nothing live changes until replaceRemoteHostAgentFiles.
func stageHostAgentUpgrade(ctx context.Context, fs *sftp.Client, target hostAgentTarget, state remoteHostAgent, binary hostAgentBinary, nonce, binaryStage, ownerStage string) error {
	if err := uploadHostAgentBinary(ctx, binary.file, binary.size, binary.hash, fs, binaryStage, nonce, target.uid); err != nil {
		return err
	}
	owner := state.owner
	owner.Hash, owner.Nonce = binary.hash, nonce
	ownerData, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return writeRemotePrivateFile(fs, ownerStage, ownerData, 0o600, nonce, target.uid)
}

// awaitUpgradedHostAgent inspects the relaunched helper, retrying briefly while
// it starts, and insists the owner on disk is the upgrade's own.
func awaitUpgradedHostAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget, binary hostAgentBinary, nonce string) (remoteHostAgent, error) {
	state, err := inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
	if err != nil {
		return state, err
	}
	if state.owner.Hash != binary.hash || state.owner.Nonce != nonce {
		return state, errHostAgentConflict
	}
	for attempt := 0; !state.status.Ready && state.ready.PID > 0 && attempt < 2; attempt++ {
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return state, ctx.Err()
		case <-timer.C:
		}
		state, err = inspectRemoteHostAgentAt(ctx, client, fs, identity, target)
		if err != nil {
			return state, err
		}
		if state.owner.Hash != binary.hash || state.owner.Nonce != nonce {
			return state, errHostAgentConflict
		}
	}
	return state, nil
}

// rollbackUpgradedHostAgent restores the previous binary and owner after the
// new version failed its health check, and relaunches the previous helper.
func rollbackUpgradedHostAgent(ctx context.Context, client *ssh.Client, fs *sftp.Client, identity Identity, target hostAgentTarget, state, previous remoteHostAgent, nonce string) (remoteHostAgent, error) {
	binaryPath := path.Join(target.stateDir, "bin", hostAgentExecutable)
	ownerPath := path.Join(target.stateDir, HostAgentOwnerFile)
	running, err := verifyOwnedHostAgentProcess(fs, target, identity, state.owner, state.ready)
	if err != nil {
		return state, err
	}
	if running {
		if err := stopOwnedHostAgentProcess(ctx, client, identity, target, state.owner, state.ready.PID); err != nil {
			return state, err
		}
	}
	if err := verifyRemoteHostAgentForUpgrade(fs, target, identity, state); err != nil {
		return state, err
	}
	if err := removeRemoteHostAgentFile(fs, path.Join(target.stateDir, HostAgentReadyFile)); err != nil {
		return state, err
	}
	failedBinary := binaryPath + ".failed-" + nonce
	failedOwner := ownerPath + ".failed-" + nonce
	if err := replaceRemoteHostAgentFiles(fs, binaryPath, ownerPath, binaryPath+".previous-"+nonce, ownerPath+".previous-"+nonce, failedBinary, failedOwner); err != nil {
		return state, err
	}
	_ = removeRemoteHostAgentFile(fs, failedBinary)
	_ = removeRemoteHostAgentFile(fs, failedOwner)
	previous.status.Ready = false
	previous.status.State = "installed"
	previous.ready = HostAgentReady{}
	if err := launchHostAgent(ctx, client, identity.HostID, target.stateDir); err != nil {
		return previous, err
	}
	return previous, fmt.Errorf("%w: la nueva versión no superó salud; se restauró la anterior", errHostAgentNotReady)
}

func verifyRemoteHostAgentForUpgrade(fs *sftp.Client, target hostAgentTarget, identity Identity, expected remoteHostAgent) error {
	ownerPath := path.Join(target.stateDir, HostAgentOwnerFile)
	ownerData, _, err := readRemotePrivateFile(fs, ownerPath, target.uid, 0o600, hostAgentMaxStateFile)
	if err != nil {
		return errHostAgentConflict
	}
	var owner HostAgentOwner
	if decodeStrictJSON(bytes.NewReader(ownerData), &owner) != nil || owner != expected.owner || owner.HostID != identity.HostID || owner.UID != target.uid {
		return errHostAgentConflict
	}
	token, _, err := readRemotePrivateFile(fs, path.Join(target.stateDir, HostAgentTokenFile), target.uid, 0o600, 128)
	if err != nil || !validHostAgentToken(token) || (len(expected.token) != 0 && string(token) != string(expected.token)) {
		return errHostAgentConflict
	}
	binaryHash, _, err := hashRemotePrivateFile(fs, path.Join(target.stateDir, "bin", owner.Executable), target.uid, 0o700)
	if err != nil || binaryHash != owner.Hash {
		return errHostAgentConflict
	}
	readyData, _, err := readRemotePrivateFile(fs, path.Join(target.stateDir, HostAgentReadyFile), target.uid, 0o600, hostAgentMaxStateFile)
	if err != nil {
		return errHostAgentConflict
	}
	var ready HostAgentReady
	if decodeStrictJSON(bytes.NewReader(readyData), &ready) != nil || ready != expected.ready {
		return errHostAgentConflict
	}
	return nil
}

func verifyOwnedHostAgentProcess(fs *sftp.Client, target hostAgentTarget, identity Identity, owner HostAgentOwner, ready HostAgentReady) (bool, error) {
	procDir := path.Join("/proc", strconv.Itoa(ready.PID))
	info, err := fs.Stat(procDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.IsDir() {
		return false, errHostAgentConflict
	}
	cmdline, err := readRemoteHostAgentFile(fs, path.Join(procDir, "cmdline"), 64<<10)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, errHostAgentConflict
	}
	if len(cmdline) == 0 {
		return false, nil
	}
	if !sameHostAgentArgs(cmdline, identity.HostID, target.stateDir) {
		return false, errHostAgentConflict
	}
	status, err := readRemoteHostAgentFile(fs, path.Join(procDir, "status"), 64<<10)
	if err != nil || !hostAgentProcessHasUID(status, target.uid) {
		return false, errHostAgentConflict
	}
	executable := path.Join(target.stateDir, "bin", owner.Executable)
	link, err := fs.ReadLink(path.Join(procDir, "exe"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || link != executable {
		return false, errHostAgentConflict
	}
	file, err := fs.Open(path.Join(procDir, "exe"))
	if err != nil {
		return false, errHostAgentConflict
	}
	hasher := sha256.New()
	n, readErr := io.Copy(hasher, io.LimitReader(file, hostAgentMaxBinary+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || n < 1 || n > hostAgentMaxBinary || hex.EncodeToString(hasher.Sum(nil)) != owner.Hash {
		return false, errHostAgentConflict
	}
	return true, nil
}

func sameHostAgentArgs(data []byte, hostID, stateDir string) bool {
	args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	expected := []string{path.Join(stateDir, "bin", hostAgentExecutable), "host-agent", "--state-dir", stateDir, "--host-id", hostID, "--ready-file", path.Join(stateDir, HostAgentReadyFile)}
	if len(args) != len(expected) {
		return false
	}
	for i := range args {
		if args[i] != expected[i] {
			return false
		}
	}
	return true
}

func hostAgentProcessHasUID(status []byte, uid int) bool {
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return false
		}
		for _, value := range fields[1:] {
			got, err := strconv.Atoi(value)
			if err != nil || got != uid {
				return false
			}
		}
		return true
	}
	return false
}

func stopOwnedHostAgentProcess(ctx context.Context, client *ssh.Client, identity Identity, target hostAgentTarget, owner HostAgentOwner, pid int) error {
	proc := "/proc/" + strconv.Itoa(pid)
	args := []string{path.Join(target.stateDir, "bin", owner.Executable), "host-agent", "--state-dir", target.stateDir, "--host-id", identity.HostID, "--ready-file", path.Join(target.stateDir, HostAgentReadyFile)}
	quotedArgs := make([]string, len(args))
	for i, arg := range args {
		quotedArgs[i] = shellQuote(arg)
	}
	command := "# workbench-owned-host-agent-stop\nset -eu\n" +
		"pid=" + shellQuote(strconv.Itoa(pid)) + "\n" +
		"proc=" + shellQuote(proc) + "\n" +
		"if [ ! -d \"$proc\" ]; then exit 0; fi\n" +
		"uid=$(awk '/^Uid:/ {print $2}' \"$proc/status\")\n" +
		"[ \"$uid\" = " + shellQuote(strconv.Itoa(target.uid)) + " ] || exit 71\n" +
		"exe=$(readlink \"$proc/exe\")\n" +
		"[ \"$exe\" = " + shellQuote(args[0]) + " ] || exit 72\n" +
		"hash=$(sha256sum \"$proc/exe\" | cut -d ' ' -f 1)\n" +
		"[ \"$hash\" = " + shellQuote(owner.Hash) + " ] || exit 73\n" +
		"cmdline=$(tr '\\000' '\\n' < \"$proc/cmdline\")\n" +
		"expected=$(printf '%s\\n' " + strings.Join(quotedArgs, " ") + ")\n" +
		"[ \"$cmdline\" = \"$expected\" ] || exit 74\n" +
		"kill -TERM \"$pid\" || { [ ! -d \"$proc\" ] && exit 0; exit 75; }\n" +
		"i=0\nwhile [ \"$i\" -lt 80 ]; do\n" +
		"  if [ ! -d \"$proc\" ]; then exit 0; fi\n" +
		"  state=$(awk '{print $3}' \"$proc/stat\" 2>/dev/null) || { [ ! -d \"$proc\" ] && exit 0; exit 77; }\n" +
		"  case \"$state\" in Z|X) exit 0;; esac\n" +
		"  sleep 0.05\n  i=$((i + 1))\ndone\nexit 76"
	if _, err := remoteCommand(ctx, client, command); err != nil {
		return fmt.Errorf("%w: la identidad del proceso cambió durante la actualización", errHostAgentConflict)
	}
	return nil
}

func replaceRemoteHostAgentFiles(fs *sftp.Client, binaryPath, ownerPath, binaryStage, ownerStage, binaryBackup, ownerBackup string) error {
	binaryMoved, ownerMoved := false, false
	rollback := func(cause error) error {
		var restore []error
		if ownerMoved {
			if err := removeRemoteHostAgentFile(fs, ownerPath); err != nil {
				restore = append(restore, err)
			}
			if err := fs.Rename(ownerBackup, ownerPath); err != nil {
				restore = append(restore, err)
			}
		}
		if binaryMoved {
			if err := removeRemoteHostAgentFile(fs, binaryPath); err != nil {
				restore = append(restore, err)
			}
			if err := fs.Rename(binaryBackup, binaryPath); err != nil {
				restore = append(restore, err)
			}
		}
		return errors.Join(append([]error{cause}, restore...)...)
	}
	if err := fs.Rename(binaryPath, binaryBackup); err != nil {
		return err
	}
	binaryMoved = true
	if err := fs.Rename(binaryStage, binaryPath); err != nil {
		return rollback(err)
	}
	if err := fs.Rename(ownerPath, ownerBackup); err != nil {
		return rollback(err)
	}
	ownerMoved = true
	if err := fs.Rename(ownerStage, ownerPath); err != nil {
		return rollback(err)
	}
	return nil
}

func removeRemoteHostAgentUpgradeBackups(fs *sftp.Client, target hostAgentTarget, nonce string) error {
	if !hostAgentNoncePattern.MatchString(nonce) {
		return errHostAgentConflict
	}
	binary := path.Join(target.stateDir, "bin", hostAgentExecutable) + ".previous-" + nonce
	owner := path.Join(target.stateDir, HostAgentOwnerFile) + ".previous-" + nonce
	return errors.Join(removeRemoteHostAgentFile(fs, binary), removeRemoteHostAgentFile(fs, owner))
}
