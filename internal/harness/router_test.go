package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOMPRequiresHostMagpieAndRejectsNativeProviderFallback(t *testing.T) {
	engines, _, bin := testEngines(t, stubRouter{})
	writeStub(t, filepath.Join(bin, "omp"))
	engines.router = nil
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly}); err == nil || !strings.Contains(err.Error(), "Magpie de este host") {
		t.Fatalf("OMP without this host's Magpie must fail closed, got %v", err)
	}
	engines.router = stubRouter{}
	if _, err := engines.Command(Spec{Engine: EngineOMP, Permission: PermissionReadOnly, Model: "openai/gpt-6-luna"}); err == nil || !strings.Contains(err.Error(), "modelos Magpie") {
		t.Fatalf("OMP native provider fallback must be rejected, got %v", err)
	}
}
