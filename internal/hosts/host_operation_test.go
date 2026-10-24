package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestExplicitFingerprintTrustAndPauseRevokeSSHOperations(t *testing.T) {
	home := t.TempDir()
	address, port, pin := sshFixture(t, home)
	m, _ := newTestModule(t)
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "fixture-host", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true, Fingerprint: pin}, Password: "fixture-secret"})
	if err != nil || host.Fingerprint != "" {
		t.Fatalf("new HostInput must not self-authorize a supplied key: %+v %v", host, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.SSH.sync(ctx, &host); err == nil || host.ObservedFingerprint != pin || host.Fingerprint != "" {
		t.Fatalf("unknown key must be observed without being trusted: %+v %v", host, err)
	}
	host.Status, host.Error = "error", "huella SSH desconocida"
	if err := m.Store.hostState(host); err != nil {
		t.Fatal(err)
	}
	observedList := moduleRequest(t, m.Handler(), http.MethodGet, "/api/hosts", "")
	var observed struct{ Hosts []hostRecord }
	if observedList.Code != http.StatusOK || json.Unmarshal(observedList.Body.Bytes(), &observed) != nil || len(observed.Hosts) != 1 {
		t.Fatalf("canonical host wrapper lost its host: %d %s", observedList.Code, observedList.Body.String())
	}
	if observed.Hosts[0].Revision != host.Revision || observed.Hosts[0].Fingerprint != "" || observed.Hosts[0].ObservedFingerprint != pin {
		t.Fatalf("host trust DTO omitted observed key/revision: %+v", observed.Hosts[0])
	}

	wrong := "SHA256:" + strings.Repeat("A", 43)
	bad := moduleRequest(t, m.Handler(), "POST", "/api/hosts/fixture-host/trust", fmt.Sprintf(`{"revision":%d,"fingerprint":%q}`, host.Revision, wrong))
	if bad.Code != 400 {
		t.Fatalf("mismatching fingerprint should be rejected: %d %s", bad.Code, bad.Body.String())
	}
	stale := moduleRequest(t, m.Handler(), "POST", "/api/hosts/fixture-host/trust", fmt.Sprintf(`{"revision":%d,"fingerprint":%q}`, host.Revision+1, pin))
	if stale.Code != 409 {
		t.Fatalf("stale fingerprint confirmation should conflict: %d %s", stale.Code, stale.Body.String())
	}
	accepted := moduleRequest(t, m.Handler(), "POST", "/api/hosts/fixture-host/trust", fmt.Sprintf(`{"revision":%d,"fingerprint":%q}`, host.Revision, pin))
	if accepted.Code != 200 {
		t.Fatalf("explicit confirmation failed: %d %s", accepted.Code, accepted.Body.String())
	}
	var trusted Host
	if err := json.Unmarshal(accepted.Body.Bytes(), &trusted); err != nil || trusted.Fingerprint != pin || trusted.ObservedFingerprint != "" || trusted.Revision != host.Revision+1 {
		t.Fatalf("confirmation did not store a new pinned revision: %+v %v", trusted, err)
	}
	root := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts", "")
	var rootRows struct{ Hosts []hostRecord }
	if err := json.Unmarshal(root.Body.Bytes(), &rootRows); err != nil || len(rootRows.Hosts) != 1 {
		t.Fatalf("canonical host wrapper changed: %s %v", root.Body.String(), err)
	}
	row := rootRows.Hosts[0]
	if !row.Paired || !row.Trusted || row.Revision != trusted.Revision || row.Fingerprint != pin || !row.Enabled || row.Status != "unknown" || row.SSHStatus != "pending" || row.HelperReady || row.GatewayReady {
		t.Fatalf("SSH trust was conflated with SSH/helper/gateway readiness: %+v", row)
	}
	identity, err := m.HostsService().Identity(ctx, host.ID)
	if err != nil || identity.Fingerprint != pin || identity.Revision != trusted.Revision || identity.Digest == "" {
		t.Fatalf("service identity did not match the confirmed key: %+v %v", identity, err)
	}

	started := make(chan Identity, 1)
	done := make(chan error, 1)
	go func() {
		done <- m.HostsService().SSHOperation(ctx, host.ID, func(opCtx context.Context, _ *ssh.Client, got Identity) error {
			started <- got
			<-opCtx.Done()
			return opCtx.Err()
		})
	}()
	select {
	case got := <-started:
		if got != identity {
			t.Fatalf("SSH operation received a different identity: %+v != %+v", got, identity)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pinned SSH operation did not reach its callback")
	}
	paused, err := m.SSH.Save(HostInput{Host: Host{ID: host.ID, Address: host.Address, Port: host.Port, User: host.User, Auth: host.Auth, Enabled: false}})
	if err != nil || paused.Status != "paused" {
		t.Fatalf("pause failed: %+v %v", paused, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pause did not cancel the active SSH operation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pause left an SSH operation live")
	}
	called := false
	if err := m.HostsService().SSHOperation(ctx, host.ID, func(context.Context, *ssh.Client, Identity) error { called = true; return nil }); !errors.Is(err, ErrPaused) || called {
		t.Fatalf("paused host was allowed to operate: called=%v err=%v", called, err)
	}
	if _, err := m.HostsService().Identity(ctx, host.ID); !errors.Is(err, ErrPaused) {
		t.Fatalf("pause kept granting host identity: %v", err)
	}

	changed, err := m.SSH.Save(HostInput{Host: Host{ID: host.ID, Address: "192.0.2.9", Port: port, User: "dev", Auth: "password", Enabled: true}})
	if err != nil || changed.Fingerprint != "" || changed.ObservedFingerprint != "" {
		t.Fatalf("endpoint edit retained the old trust pin: %+v %v", changed, err)
	}
	if _, err := m.HostsService().Identity(ctx, host.ID); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("endpoint edit retained old authority: %v", err)
	}
}

func TestObservedChangedFingerprintQuarantinesIdentityAndSSHOperations(t *testing.T) {
	home := t.TempDir()
	address, port, pin := sshFixture(t, home)
	m, _ := newTestModule(t)
	defer m.Close()
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "changed-host", Address: address, Port: port, User: "dev", Auth: "password", Enabled: true}, Password: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trustSSHFixture(t, m.SSH, ctx, &host, pin)
	changed := "SHA256:" + strings.Repeat("A", 43)
	if changed == pin {
		changed = "SHA256:" + strings.Repeat("B", 43)
	}
	host.ObservedFingerprint = changed
	host.Status, host.Error = "error", "huella SSH cambió"
	if err := m.Store.hostState(host); err != nil {
		t.Fatal(err)
	}
	if _, err := m.HostsService().Identity(ctx, host.ID); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("changed key observation did not invalidate identity: %v", err)
	}
	called := false
	err = m.HostsService().SSHOperation(ctx, host.ID, func(context.Context, *ssh.Client, Identity) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrIdentityChanged) || called {
		t.Fatalf("changed key observation allowed an SSH operation: called=%v err=%v", called, err)
	}
	root := moduleRequest(t, m.HostsHandler(), http.MethodGet, "/api/hosts", "")
	var rows struct{ Hosts []hostRecord }
	if err := json.Unmarshal(root.Body.Bytes(), &rows); err != nil || len(rows.Hosts) != 1 {
		t.Fatalf("changed host missing from canonical wrapper: %s %v", root.Body.String(), err)
	}
	row := rows.Hosts[0]
	if !row.Paired || row.Trusted || row.HelperReady || row.GatewayReady || row.Fingerprint != pin || row.ObservedFingerprint != changed {
		t.Fatalf("changed pin/readiness states were conflated: %+v", row)
	}
}
