package app

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"
)

func TestTrustedProxyNativeInferenceDoesNotNeedBrowserOrigin(t *testing.T) {
	cfg := testConfig(t)
	cfg.PublicURL = "https://workbench.example"
	cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	models := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.URL.Path)
	})
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Models: models}))
	if err != nil {
		t.Fatal(err)
	}
	request := newRequest(http.MethodPost, cfg.PublicURL+"/v1/chat/completions", "workbench.example", nil, "")
	request.TLS = nil
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("Authorization", "Bearer independent-gateway-key")
	response := serve(handler.Handler(), request)
	if response.Code != http.StatusOK || response.Body.String() != "/v1/chat/completions" {
		t.Fatalf("trusted native inference = %d %q", response.Code, response.Body.String())
	}
}
