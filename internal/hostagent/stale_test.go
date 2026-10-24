package hostagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"workbench/internal/hosts"
)

func TestStaleReadyWithRecycledPIDDoesNotBlockRestart(t *testing.T) {
	self, err := hashFile("/proc/self/exe")
	if err != nil {
		t.Skip("no /proc:", err)
	}
	owner := hosts.HostAgentOwner{HostID: "h", UID: os.Geteuid(), Hash: self, Nonce: "n"}
	path := filepath.Join(t.TempDir(), "ready.json")
	write := func(pid int) {
		data, _ := json.Marshal(hosts.HostAgentReady{HostID: "h", UID: os.Geteuid(), Hash: self, Nonce: "n", PID: pid, Port: 4000})
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A live process running the owner's binary still blocks a second helper.
	write(os.Getpid())
	if err := removeStaleReady(path, owner); err == nil {
		t.Fatal("running helper did not block restart")
	}
	// The same live PID under a different binary hash is a recycled PID: stale.
	other := owner
	other.Hash = "0000"
	write(os.Getpid())
	data, _ := os.ReadFile(path)
	var ready hosts.HostAgentReady
	_ = json.Unmarshal(data, &ready)
	ready.Hash = other.Hash
	data, _ = json.Marshal(ready)
	_ = os.WriteFile(path, data, 0o600)
	if err := removeStaleReady(path, other); err != nil {
		t.Fatalf("recycled PID blocked restart: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("stale receipt not removed")
	}
}
