package app

import (
	"net/http"
	"strings"
	"testing"
)

// PL-12: el documento del shell declara su propia política; los assets, la API y los
// sitios nativos no reciben una segunda CSP desde esta capa.
func TestShellDocumentCarriesItsOwnPolicy(t *testing.T) {
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{}))
	if err != nil {
		t.Fatal(err)
	}
	required := []string{
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self'",
		"font-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'self'",
	}
	for _, path := range []string{"/", "/settings", "/instalacion/profunda/no-esta-en-el-router"} {
		request := newRequest(http.MethodGet, cfg.PublicURL+path, mustURLHost(t, cfg.PublicURL), nil, "")
		response := serve(handler.Handler(), request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d, want the shell document", path, response.Code)
		}
		policy := response.Header().Get("Content-Security-Policy")
		if policy == "" {
			t.Fatalf("%s: el shell no declara Content-Security-Policy", path)
		}
		for _, directive := range required {
			if !strings.Contains(policy, directive) {
				t.Fatalf("%s: falta %q en la política %q", path, directive, policy)
			}
		}
		// Los orígenes de los frames Magpie/SIM se asignan por host en runtime: la
		// política del shell no puede fijarlos con default-src ni con frame-src.
		for _, forbidden := range []string{"default-src", "frame-src", "unsafe-eval", "*"} {
			if strings.Contains(policy, forbidden) {
				t.Fatalf("%s: la política no debe contener %q: %q", path, forbidden, policy)
			}
		}
	}

	request := newRequest(http.MethodGet, cfg.PublicURL+"/assets/app.js", mustURLHost(t, cfg.PublicURL), nil, "")
	if response := serve(handler.Handler(), request); response.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("an asset carries the shell policy: %q", response.Header().Get("Content-Security-Policy"))
	}
	request = newRequest(http.MethodGet, cfg.PublicURL+"/api/bootstrap", mustURLHost(t, cfg.PublicURL), nil, "")
	if response := serve(handler.Handler(), request); response.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("an API response carries the shell policy: %q", response.Header().Get("Content-Security-Policy"))
	}
}
