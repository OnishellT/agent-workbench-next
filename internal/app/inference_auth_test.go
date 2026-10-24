package app

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInferenceAPIRequiresSeparateBearerAuthority(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	models := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, r.URL.Path)
	})
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Models: models}))
	if err != nil {
		t.Fatal(err)
	}
	browserCookie := authorize(t, handler.Handler(), cfg)

	request := newRequest(http.MethodPost, cfg.PublicURL+"/v1/chat/completions", "127.0.0.1:18888", nil, "")
	addCookie(request, browserCookie)
	if response := serve(handler.Handler(), request); response.Code != http.StatusUnauthorized || calls.Load() != 0 {
		t.Fatalf("browser cookie authorized inference: status=%d calls=%d", response.Code, calls.Load())
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/v1/chat/completions", "127.0.0.1:18888", strings.NewReader("{}"), "")
	request.Header.Set("Authorization", "Bearer independent-gateway-key")
	if response := serve(handler.Handler(), request); response.Code != http.StatusOK || response.Body.String() != "/v1/chat/completions" || calls.Load() != 1 {
		t.Fatalf("native Bearer route = status %d calls %d body %q", response.Code, calls.Load(), response.Body.String())
	}

	request = newRequest(http.MethodPost, cfg.PublicURL+"/v1/chat/completions", "127.0.0.1:18888", nil, "")
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	if response := serve(handler.Handler(), request); response.Code != http.StatusUnauthorized || calls.Load() != 1 {
		t.Fatalf("browser token converted to inference authority: status=%d calls=%d", response.Code, calls.Load())
	}
}

func TestInferenceRouteRejectsUnsupportedPathsAndQueries(t *testing.T) {
	cfg := testConfig(t)
	var calls atomic.Int32
	models := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Models: models}))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		cfg.PublicURL + "/v1/private?bad=value",
		cfg.PublicURL + "/v1beta/models/../model:generateContent",
		cfg.PublicURL + "/v1/chat/completions?alt=no",
	} {
		request := newRequest(http.MethodPost, target, "127.0.0.1:18888", nil, "")
		request.Header.Set("Authorization", "Bearer independent-gateway-key")
		if response := serve(handler.Handler(), request); response.Code != http.StatusUnauthorized {
			t.Errorf("unsupported machine request %s status=%d, want 401", target, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("unsupported machine routes reached Models handler %d times", calls.Load())
	}
}
