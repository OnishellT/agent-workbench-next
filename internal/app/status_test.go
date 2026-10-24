package app

import "testing"

func TestAppStatusReflectsMountedModules(t *testing.T) {
	s := &Server{}
	modules := map[string]string{"ui": "mounted", "models": "mounted", "foundry": "mounted", "settings": "mounted", "platform_hosts": "mounted", "module_diagnostics": "mounted"}
	if got := s.appStatus(modules); got != "ok" {
		t.Fatalf("complete wiring reported %q", got)
	}
	modules["foundry"] = "not_implemented"
	if got := s.appStatus(modules); got != "partial" {
		t.Fatalf("missing feature reported %q", got)
	}
	modules["models"] = "not_implemented"
	if got := s.appStatus(modules); got != "degraded" {
		t.Fatalf("missing core module reported %q", got)
	}
}
