package hosts

import "testing"

func TestHostValidationAcceptsIPAndDNSAndRejectsMalformedAddresses(t *testing.T) {
	for _, address := range []string{"192.0.2.8", "2001:db8::8", "localhost", "host-1.example.test", "example.test."} {
		host := Host{ID: "test-host", Address: address, User: "operator", Port: 22, Auth: "agent"}
		if err := host.Validate(); err != nil {
			t.Errorf("valid host address %q rejected: %v", address, err)
		}
	}
	for _, address := range []string{"", "127.0.0.999", "999.999.999.999", "192..0.2", "-host.example", "host-.example", "bad..example", "host name", "example.test:22"} {
		host := Host{ID: "test-host", Address: address, User: "operator", Port: 22, Auth: "agent"}
		if err := host.Validate(); err == nil {
			t.Errorf("malformed host address %q accepted", address)
		}
	}
	host := Host{ID: "test-host", Name: "host\nspoofed", Address: "example.test", User: "operator", Port: 22, Auth: "agent"}
	if err := host.Validate(); err == nil {
		t.Fatal("host name containing a line break was accepted")
	}
}
