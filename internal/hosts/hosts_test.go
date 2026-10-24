package hosts

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestHostIdentityMatchesLegacyTupleAndRejectsUnconfirmed(t *testing.T) {
	h := Host{ID: "remote-1", Address: "10.8.0.3", Port: 2222, User: "operator", Fingerprint: "SHA256:fixture", Revision: 9}
	got, err := h.Identity()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := sha256.Sum256([]byte("[\"remote-1\",\"10.8.0.3\",2222,\"operator\",\"SHA256:fixture\",9]"))
	if got.Digest != hex.EncodeToString(wantBytes[:]) || got.HostID != h.ID || got.Revision != h.Revision || got.Address != h.Address || got.User != h.User || got.Port != h.Port || got.Fingerprint != h.Fingerprint {
		t.Fatalf("identity tuple drift: %+v", got)
	}
	for _, invalid := range []Host{
		{ID: "remote-1", Address: h.Address, User: h.User, Port: h.Port, Revision: h.Revision},
		{ID: "local", Address: h.Address, User: h.User, Port: h.Port, Fingerprint: h.Fingerprint, Revision: h.Revision},
		{ID: h.ID, Address: h.Address, User: h.User, Port: 0, Fingerprint: h.Fingerprint, Revision: h.Revision},
		{ID: h.ID, Address: h.Address, User: h.User, Port: h.Port, Fingerprint: h.Fingerprint, Revision: 0},
	} {
		if _, err := invalid.Identity(); err == nil {
			t.Fatalf("accepted incomplete identity: %+v", invalid)
		}
	}
}
