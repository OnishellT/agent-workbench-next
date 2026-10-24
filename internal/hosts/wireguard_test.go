package hosts

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestInspectWireGuardRouteUsesOnlyReadCommands(t *testing.T) {
	calls := [][]string{}
	read := func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		switch {
		case reflect.DeepEqual(args, []string{"link", "show", "type", "wireguard"}):
			return []byte(`[{"ifname":"wg0","mtu":1420}]`), nil
		case reflect.DeepEqual(args, []string{"-4", "route", "get", "10.8.0.4"}):
			return []byte(`[{"dev":"wg0"}]`), nil
		case reflect.DeepEqual(args, []string{"-4", "route", "show", "default"}):
			return []byte(`[{"dev":"wg0"}]`), nil
		default:
			return nil, fmt.Errorf("unexpected route query: %v", args)
		}
	}
	result, err := inspectWireGuardRoute(context.Background(), Host{ID: "host-a", Name: "Test host", Address: "10.8.0.4"}, read)
	if err != nil {
		t.Fatal(err)
	}
	if result.HostID != "host-a" || result.Address != "10.8.0.4" || result.Interface == nil || *result.Interface != "wg0" || !result.WireGuard || result.MTU == nil || *result.MTU != 1420 || !result.MainDefaultUsesWG || result.Scope != "route-only" || result.ConfigurationChanged {
		t.Fatalf("unexpected route diagnostic: %+v", result)
	}
	if len(calls) != 3 {
		t.Fatalf("expected only link-show/route-get/route-show queries, got %v", calls)
	}
}

func TestInspectWireGuardRejectsAmbiguousAndUnsafeAddressesBeforeSystemQueries(t *testing.T) {
	for _, address := range []string{"", "host.example", "127.0.0.1", "::1", "0.0.0.0", "ff02::1"} {
		t.Run(address, func(t *testing.T) {
			calls := 0
			read := func(context.Context, ...string) ([]byte, error) {
				calls++
				return []byte(`[]`), nil
			}
			if _, err := inspectWireGuardRoute(context.Background(), Host{Address: address}, read); err == nil {
				t.Fatalf("accepted unsafe or ambiguous address %q", address)
			}
			if calls != 0 {
				t.Fatalf("executed route query for invalid target %q", address)
			}
		})
	}
}

func TestWireGuardDiagnosticReportsMissingIPRoute2(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := runIPJSON(context.Background(), "link", "show", "type", "wireguard"); err == nil || err.Error() != "iproute2 no está disponible para consultar la ruta" {
		t.Fatalf("missing iproute2 was not reported safely: %v", err)
	}
}
