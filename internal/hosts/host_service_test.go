// SPDX-License-Identifier: AGPL-3.0-only
package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestHostServiceAndCanonicalProjectionShareRecordsWithoutSecrets(t *testing.T) {
	m, _ := newTestModule(t)
	service := m.HostsService()
	if service == nil || service != m.HostsService() {
		t.Fatal("Module must return one stable Hosts service instance")
	}
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "pending-host", Name: "Pending", Address: "192.0.2.7", Port: 22, User: "dev", Auth: "password", Enabled: true, Fingerprint: "SHA256:caller-supplied"}, Password: "new-private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if host.Fingerprint != "" || host.ObservedFingerprint != "" {
		t.Fatalf("caller-supplied fingerprint bypassed explicit trust: %+v", host)
	}
	ctx := context.Background()
	rows, err := service.List(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != host.ID {
		t.Fatalf("List did not use the canonical hosts table: %+v %v", rows, err)
	}
	got, err := service.Get(ctx, host.ID)
	if err != nil || got.Address != host.Address || got.User != host.User || got.Port != host.Port || got.Revision != host.Revision {
		t.Fatalf("Get lost host identity: %+v %v", got, err)
	}
	if _, err := service.Identity(ctx, host.ID); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("untrusted host identity should be blocked, got %v", err)
	}

	root := moduleRequest(t, m.Handler(), http.MethodGet, "/api/hosts", "")
	var projection struct{ Hosts []hostRecord }
	if root.Code != http.StatusOK || json.Unmarshal(root.Body.Bytes(), &projection) != nil || len(projection.Hosts) != 1 {
		t.Fatalf("canonical host wrapper changed: %d %s", root.Code, root.Body.String())
	}
	row := projection.Hosts[0]
	if row.ID != host.ID || row.Address != host.Address || row.User != host.User || row.Port != host.Port || row.Auth != host.Auth || !row.PasswordSet || !row.Enabled || row.Status != "unknown" || !row.Paired || row.Transport != "ssh" || row.SSHStatus != "pending" || row.Revision != host.Revision || row.Fingerprint != "" || row.ObservedFingerprint != "" || row.Trusted || row.HelperReady || row.GatewayReady {
		t.Fatalf("projection lost registration fields or conflated readiness/trust: %+v", row)
	}
	var body struct{ Hosts []map[string]json.RawMessage }
	if err := json.Unmarshal(root.Body.Bytes(), &body); err != nil || len(body.Hosts) != 1 {
		t.Fatalf("host response JSON: %v", err)
	}
	for _, key := range []string{"password", "requests", "total_tokens", "cost_usd"} {
		if _, exists := body.Hosts[0][key]; exists {
			t.Fatalf("host API exposed retired or secret field %q", key)
		}
	}
	if strings.Contains(root.Body.String(), "new-private-secret") {
		t.Fatal("root host projection leaked the encrypted credential")
	}
	if response := moduleRequest(t, m.Handler(), http.MethodGet, "/api/network/wireguard?host_id=missing", ""); response.Code != http.StatusNotFound {
		t.Fatalf("WireGuard diagnostics accepted an unknown host: %d %s", response.Code, response.Body.String())
	}
	if response := moduleRequest(t, m.Handler(), http.MethodGet, "/api/hosts/pending-host/history", ""); response.Code != http.StatusOK {
		t.Fatalf("history consent status should be available: %d %s", response.Code, response.Body.String())
	} else {
		var status hostHistoryStatus
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.Enabled || status.State != "untrusted" {
			t.Fatalf("untrusted host history state: %+v %v", status, err)
		}
	}

	paused, err := m.SSH.Save(HostInput{Host: Host{ID: host.ID, Address: host.Address, Port: host.Port, User: host.User, Auth: host.Auth, Enabled: false}})
	if err != nil || paused.Status != "paused" {
		t.Fatalf("pause did not persist: %+v %v", paused, err)
	}
	if _, err := service.Identity(ctx, host.ID); !errors.Is(err, ErrPaused) {
		t.Fatalf("paused host still granted identity authority: %v", err)
	}
}

func TestHostCreateRejectsDuplicateIDWithoutReplacingRecord(t *testing.T) {
	m, _ := newTestModule(t)
	defer m.Close()
	first, err := m.SSH.Save(HostInput{Host: Host{ID: "duplicate-host", Name: "Original", Address: "192.0.2.12", Port: 22, User: "operator", Auth: "password", Enabled: false}, Password: "private-test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	secretBefore, err := m.Store.hostSecret(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	createBody, err := json.Marshal(HostInput{Host: Host{ID: first.ID, Name: "Replacement", Address: "192.0.2.13", Port: 2222, User: "other", Auth: "password", Enabled: false}, Password: "other-private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := moduleRequest(t, m.Handler(), http.MethodPost, "/api/hosts", string(createBody))
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate create did not conflict: %d %s", duplicate.Code, duplicate.Body.String())
	}
	stored, err := m.Store.Host(first.ID)
	if err != nil || stored.Name != "Original" || stored.Address != "192.0.2.12" || stored.Port != 22 || stored.User != "operator" || stored.Revision != first.Revision {
		t.Fatalf("duplicate create replaced the existing record: %+v %v", stored, err)
	}
	secretAfter, err := m.Store.hostSecret(first.ID)
	if err != nil || string(secretAfter) != string(secretBefore) {
		t.Fatalf("duplicate create replaced the stored credential: %v", err)
	}
	freshBody, err := json.Marshal(HostInput{Host: Host{Name: "Fresh", Address: "192.0.2.14", Port: 22, User: "operator", Auth: "password", Enabled: false}, Password: "fresh-private-secret"})
	if err != nil {
		t.Fatal(err)
	}
	fresh := moduleRequest(t, m.Handler(), http.MethodPost, "/api/hosts", string(freshBody))
	if fresh.Code != http.StatusCreated {
		t.Fatalf("normal create without an explicit id failed: %d %s", fresh.Code, fresh.Body.String())
	}
	editBody, err := json.Marshal(HostInput{Host: Host{ID: first.ID, Name: "Edited", Address: "192.0.2.12", Port: 22, User: "operator", Auth: "password", Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	edited := moduleRequest(t, m.Handler(), http.MethodPut, "/api/hosts/duplicate-host", string(editBody))
	if edited.Code != http.StatusOK {
		t.Fatalf("edit after duplicate rejection failed: %d %s", edited.Code, edited.Body.String())
	}
}

func TestHostEditUsesPathIdentityAndKeepsEncryptedCredential(t *testing.T) {
	m, _ := newTestModule(t)
	defer m.Close()
	host, err := m.SSH.Save(HostInput{Host: Host{ID: "edit-host", Name: "Before", Address: "192.0.2.10", Port: 22, User: "operator", Auth: "password", Enabled: false}, Password: "private-test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	secretBefore, err := m.Store.hostSecret(host.ID)
	if err != nil {
		t.Fatal(err)
	}
	editBody, err := json.Marshal(HostInput{Host: Host{ID: host.ID, Name: "After", Address: "192.0.2.10", Port: 22, User: "operator", Auth: "password", Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	updated := moduleRequest(t, m.Handler(), http.MethodPut, "/api/hosts/edit-host", string(editBody))
	var saved Host
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &saved) != nil || saved.ID != host.ID || saved.Name != "After" || saved.Revision != host.Revision+1 || !saved.PasswordSet {
		t.Fatalf("host edit contract failed: %d %s", updated.Code, updated.Body.String())
	}
	secretAfter, err := m.Store.hostSecret(host.ID)
	if err != nil || string(secretAfter) != string(secretBefore) {
		t.Fatal("editing public host fields replaced its stored encrypted credential", err)
	}
	mismatch, err := json.Marshal(map[string]string{"id": "other-host"})
	if err != nil {
		t.Fatal(err)
	}
	response := moduleRequest(t, m.Handler(), http.MethodPut, "/api/hosts/edit-host", string(mismatch))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("host body escaped the path identity: %d %s", response.Code, response.Body.String())
	}
	after, err := m.Store.Host(host.ID)
	if err != nil || after.Name != "After" || after.Revision != saved.Revision {
		t.Fatalf("mismatched edit mutated host record: %+v %v", after, err)
	}
}
