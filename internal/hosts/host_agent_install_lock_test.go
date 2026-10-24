package hosts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A failed install must leave neither status nor retry wedged: an orphaned
// .install.lock (timeout, dropped connection, restarted Workbench) self-heals
// on the next inspect, while a live lock keeps its 409. Ownership files are
// never touched by the recovery.
func TestStaleInstallLockSelfHealsAndRetryProceeds(t *testing.T) {
	setNativeMagpieTestBinary(t)
	home := t.TempDir()
	address, port, pin, launched := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	handler := m.HostsHandler()

	installed := moduleRequest(t, handler, http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision))
	if installed.Code != http.StatusAccepted {
		t.Fatalf("initial fixture install status = %d: %s", installed.Code, installed.Body.String())
	}
	select {
	case <-launched:
	case <-time.After(2 * time.Second):
		t.Fatal("initial helper launch missing")
	}
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	lockPath := filepath.Join(stateDir, ".install.lock")
	ownerPath := filepath.Join(stateDir, HostAgentOwnerFile)
	ownerBefore, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}

	plantLock := func() {
		t.Helper()
		if err := os.WriteFile(lockPath, []byte("orphaned-install-lock"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plantLock()

	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if status.Code != http.StatusOK || !contains(status.Body.String(), `"state":"installing"`) {
		t.Fatalf("live lock must report installing: %d %s", status.Code, status.Body.String())
	}
	blocked := moduleRequest(t, handler, http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision))
	if blocked.Code != http.StatusConflict {
		t.Fatalf("live lock install = %d, want 409: %s", blocked.Code, blocked.Body.String())
	}
	if _, err := os.Lstat(lockPath); err != nil {
		t.Fatalf("live lock was removed too eagerly: %v", err)
	}

	// A 10-minute-old lock is still a live install under the 35-minute stale
	// age (a slow upload may legitimately hold it): it must not be deleted.
	fresh := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(lockPath, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	held := moduleRequest(t, handler, http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if held.Code != http.StatusOK || !contains(held.Body.String(), `"state":"installing"`) {
		t.Fatalf("10-minute lock must still report installing: %d %s", held.Code, held.Body.String())
	}
	if _, err := os.Lstat(lockPath); err != nil {
		t.Fatalf("live 10-minute lock was removed: %v", err)
	}
	stale := time.Now().Add(-40 * time.Minute)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	healed := moduleRequest(t, handler, http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if healed.Code != http.StatusOK || contains(healed.Body.String(), `"state":"installing"`) {
		t.Fatalf("stale lock must self-heal on status: %d %s", healed.Code, healed.Body.String())
	}
	if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("stale lock survived status: %v", err)
	}
	ownerAfter, err := os.ReadFile(ownerPath)
	if err != nil || string(ownerAfter) != string(ownerBefore) {
		t.Fatalf("lock recovery touched ownership: %v", err)
	}

	retry := moduleRequest(t, handler, http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision))
	if retry.Code == http.StatusConflict {
		t.Fatalf("retry after stale lock still wedged: %d %s", retry.Code, retry.Body.String())
	}
	if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("retry left its lock behind: %v", err)
	}
}

// An unverifiable lock is never deleted: recovery fails closed and keeps the
// conflict instead of removing something it cannot own.
func TestUnverifiableInstallLockIsKept(t *testing.T) {
	setNativeMagpieTestBinary(t)
	home := t.TempDir()
	address, port, pin, launched := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	handler := m.HostsHandler()

	installed := moduleRequest(t, handler, http.MethodPost, "/api/hosts/fixture-agent/magpie/install", fmt.Sprintf(`{"revision":%d}`, host.Revision))
	if installed.Code != http.StatusAccepted {
		t.Fatalf("initial fixture install status = %d: %s", installed.Code, installed.Body.String())
	}
	select {
	case <-launched:
	case <-time.After(2 * time.Second):
		t.Fatal("initial helper launch missing")
	}
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	lockPath := filepath.Join(stateDir, ".install.lock")
	if err := os.WriteFile(lockPath, []byte("foreign-lock"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	status := moduleRequest(t, handler, http.MethodGet, "/api/hosts/fixture-agent/magpie/status", "")
	if status.Code == http.StatusOK && contains(status.Body.String(), `"installed":true`) {
		t.Fatalf("unverifiable lock was cleared: %d %s", status.Code, status.Body.String())
	}
	if _, err := os.Lstat(lockPath); err != nil {
		t.Fatalf("unverifiable lock was removed: %v", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// A timed-out first install leaves bin/workbench plus bin/magpie.tmp-<nonce>
// with no owner and (after the stale reaper) no lock; the retry met an
// unattributable 409. Transport death is not reproducible in-fixture, so the
// cutoff is planted as the exact disk state the marker flow would leave, and
// the retry must recover exactly that transaction and finish the install.
func plantInstallCutoff(t *testing.T, stateDir, nonce string, markerPaths []string, markerMode os.FileMode, extra map[string]string) {
	t.Helper()
	bin := filepath.Join(stateDir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// Complete first upload, as the timeout leaves it.
	if err := os.WriteFile(filepath.Join(bin, "workbench"), []byte("owned native Magpie binary fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Partial second upload: the staging tmp a dead transport orphans.
	if err := os.WriteFile(filepath.Join(bin, "magpie.tmp-"+nonce), []byte("half-written magpie"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range extra {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	claim, err := json.Marshal(map[string]any{"nonce": nonce, "paths": markerPaths})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".install.partial"), claim, markerMode); err != nil {
		t.Fatal(err)
	}
}

func cutoffMarkerPaths(nonce string) []string {
	return []string{
		"bin",
		"bin/workbench",
		"bin/workbench.tmp-" + nonce,
		"bin/magpie",
		"bin/magpie.tmp-" + nonce,
		"magpie.owner",
		"magpie.owner.tmp-" + nonce,
		"access.token",
		"access.token.tmp-" + nonce,
		"agent.owner.tmp-" + nonce,
	}
}

const cutoffNonce = "0123456789abcdef0123456789abcdef"

func setupCutoffFixture(t *testing.T) (http.Handler, string, int64) {
	t.Helper()
	setNativeMagpieTestBinary(t)
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	m, _ := newTestModule(t)
	t.Cleanup(func() { _ = m.Close() })
	host := trustedHostAgentFixture(t, m, address, port, pin)
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return m.HostsHandler(), stateDir, host.Revision
}

func installRoute(t *testing.T, handler http.Handler, hostID string, revision int64) (int, string) {
	t.Helper()
	response := moduleRequest(t, handler, http.MethodPost, "/api/hosts/"+hostID+"/magpie/install", fmt.Sprintf(`{"revision":%d}`, revision))
	return response.Code, response.Body.String()
}

// The observed V2 leftovers with their pre-upload marker retry into a full
// install: no 409, own staging names replaced, ownership written.
func TestPartialFirstInstallRecoversOnRetry(t *testing.T) {
	handler, stateDir, revision := setupCutoffFixture(t)
	plantInstallCutoff(t, stateDir, cutoffNonce, cutoffMarkerPaths(cutoffNonce), 0o600, nil)

	code, body := installRoute(t, handler, "fixture-agent", revision)
	if code == http.StatusConflict {
		t.Fatalf("marked partial must not wedge the retry: %d %s", code, body)
	}
	if code != http.StatusAccepted {
		t.Fatalf("retry install status = %d: %s", code, body)
	}
	if !contains(body, `"installed":true`) {
		t.Fatalf("retry did not finish the install: %s", body)
	}
	for _, leftover := range []string{".install.lock", ".install.partial", "magpie.tmp-" + cutoffNonce} {
		matches, _ := filepath.Glob(filepath.Join(stateDir, "bin", leftover))
		if len(matches) != 0 {
			t.Fatalf("leftover survived the retry: %v", matches)
		}
		if _, err := os.Lstat(filepath.Join(stateDir, leftover)); !os.IsNotExist(err) {
			t.Fatalf("leftover survived the retry: %s", leftover)
		}
	}
	owner, err := os.ReadFile(filepath.Join(stateDir, "agent.owner"))
	if err != nil || !contains(string(owner), `"host_id":"fixture-agent"`) {
		t.Fatalf("retry left no valid ownership: %v %s", err, owner)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, "bin", "workbench")); err != nil {
		t.Fatalf("fresh binary missing after retry: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(stateDir, "bin", "magpie")); err != nil {
		t.Fatalf("fresh magpie missing after retry: %v", err)
	}
}

// A foreign file next to the marked partial is never touched, but the install
// still refuses: the retry 409s on the unattributable remainder after rolling
// back exactly its own names.
func TestPartialFirstInstallWithForeignFileStaysConflict(t *testing.T) {
	handler, stateDir, revision := setupCutoffFixture(t)
	plantInstallCutoff(t, stateDir, cutoffNonce, cutoffMarkerPaths(cutoffNonce), 0o600,
		map[string]string{"ajeno.txt": "not from any install transaction"})
	code, body := installRoute(t, handler, "fixture-agent", revision)
	if code != http.StatusConflict || !contains(body, "no atribuible") {
		t.Fatalf("foreign remainder must stay 409: %d %s", code, body)
	}
	foreign, err := os.ReadFile(filepath.Join(stateDir, "ajeno.txt"))
	if err != nil || string(foreign) != "not from any install transaction" {
		t.Fatalf("foreign file was touched: %v %s", err, foreign)
	}
	for _, rel := range []string{"bin/workbench", "bin/magpie.tmp-" + cutoffNonce} {
		if _, err := os.Lstat(filepath.Join(stateDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("own staging name survived its rollback: %s", rel)
		}
	}
}

// Unmarked leftovers are preexisting ambiguous state: the retry 409s and
// deletes nothing.
func TestUnmarkedPartialStaysConflict(t *testing.T) {
	handler, stateDir, revision := setupCutoffFixture(t)
	bin := filepath.Join(stateDir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte("owned native Magpie binary fixture")
	if err := os.WriteFile(filepath.Join(bin, "workbench"), before, 0o700); err != nil {
		t.Fatal(err)
	}
	tmpName := "magpie.tmp-ffffffffffffffffffffffffffffffff"
	if err := os.WriteFile(filepath.Join(bin, tmpName), []byte("half-written magpie"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, body := installRoute(t, handler, "fixture-agent", revision)
	if code != http.StatusConflict || !contains(body, "no atribuible") {
		t.Fatalf("unmarked partial must stay 409: %d %s", code, body)
	}
	kept, err := os.ReadFile(filepath.Join(bin, "workbench"))
	if err != nil || string(kept) != string(before) {
		t.Fatalf("unmarked state was touched: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(bin, tmpName)); err != nil {
		t.Fatalf("unmarked tmp was touched: %v", err)
	}
}

// A marker that claims protected or foreign names voids the whole claim: the
// retry 409s and every planted byte survives, including the marker.
func TestHostileMarkerKeepsEverything(t *testing.T) {
	for _, paths := range [][]string{
		{"agent.owner"},
		{"../escape.txt"},
		{"bin/workbench", "agent.ready"},
	} {
		handler, stateDir, revision := setupCutoffFixture(t)
		plantInstallCutoff(t, stateDir, cutoffNonce, paths, 0o600, nil)
		code, body := installRoute(t, handler, "fixture-agent", revision)
		if code != http.StatusConflict {
			t.Fatalf("hostile marker %v must stay 409: %d %s", paths, code, body)
		}
		for _, rel := range []string{"bin/workbench", "bin/magpie.tmp-" + cutoffNonce, ".install.partial"} {
			if _, err := os.Lstat(filepath.Join(stateDir, rel)); err != nil {
				t.Fatalf("hostile marker %v deleted %s: %v", paths, rel, err)
			}
		}
	}
}

// An unverifiable marker (wrong mode) voids the claim the same way.
func TestUnverifiableMarkerKeepsEverything(t *testing.T) {
	handler, stateDir, revision := setupCutoffFixture(t)
	plantInstallCutoff(t, stateDir, cutoffNonce, cutoffMarkerPaths(cutoffNonce), 0o644, nil)
	code, _ := installRoute(t, handler, "fixture-agent", revision)
	if code != http.StatusConflict {
		t.Fatalf("unverifiable marker must stay 409: %d", code)
	}
	for _, rel := range []string{"bin/workbench", "bin/magpie.tmp-" + cutoffNonce, ".install.partial"} {
		if _, err := os.Lstat(filepath.Join(stateDir, rel)); err != nil {
			t.Fatalf("unverifiable marker deleted %s: %v", rel, err)
		}
	}
}

// The install deadline scales with the release payloads instead of
// truncating slow-link uploads mid-copy.
func TestHostAgentInstallTimeoutScales(t *testing.T) {
	if got := hostAgentInstallTimeout(0); got != 3*time.Minute {
		t.Fatalf("empty timeout = %v, want floor 3m", got)
	}
	if got := hostAgentInstallTimeout(-5); got != 3*time.Minute {
		t.Fatalf("negative timeout = %v, want floor 3m", got)
	}
	got := hostAgentInstallTimeout(85 << 20)
	if got < 10*time.Minute || got > 20*time.Minute {
		t.Fatalf("85MB timeout = %v, want within 10-20m", got)
	}
	if got := hostAgentInstallTimeout(1 << 40); got != 30*time.Minute {
		t.Fatalf("huge timeout = %v, want ceiling 30m", got)
	}
}
