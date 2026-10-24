package hosts

import "testing"

func TestHostAgentStateDirUsesNamespacedNewStateRoot(t *testing.T) {
	got, err := HostAgentStateDir("/remote/state", "host_17")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/remote/state/agent-workbench-next/host-agent/host_17"; got != want {
		t.Fatalf("state dir = %q, want %q", got, want)
	}
}

func TestHostAgentStateDirRejectsUnsafeRootAndHostID(t *testing.T) {
	for _, tc := range []struct {
		root string
		id   string
	}{
		{root: "relative", id: "host"},
		{root: "/remote/state/../other", id: "host"},
		{root: "/remote/state", id: "local"},
		{root: "/remote/state", id: "../other"},
		{root: "/remote/state", id: "with/slash"},
	} {
		if _, err := HostAgentStateDir(tc.root, tc.id); err == nil {
			t.Errorf("HostAgentStateDir(%q,%q) accepted invalid input", tc.root, tc.id)
		}
	}
}
