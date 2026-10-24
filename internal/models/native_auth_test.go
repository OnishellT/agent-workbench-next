package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestPublicGatewayRequiresNativeCallerKeyAndPreservesBearer(t *testing.T) {
	const nativeKey = "fixture-native-public-key-0123456789"
	backendCalls := 0
	forwarded := ""
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls++
		forwarded = r.Header.Get("X-Forwarded-For")
		if got := r.Header.Get("Authorization"); got != "Bearer "+nativeKey {
			t.Errorf("native bearer = %q, want the validated key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer backend.Close()

	expectedCookie, expectedOrigin := "", ""
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != expectedCookie || r.Header.Get("Origin") != expectedOrigin {
			t.Errorf("private native web request lacks its scoped cookie/origin")
		}
		switch r.URL.Path {
		case "/api/caller-keys":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"keys":[{"id":"public","masked":"…6789","internal":false},{"id":"internal","masked":"…cdef","internal":true}]}`))
		case "/api/caller-keys/copy-key":
			var in struct {
				Key string `json:"key"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				http.Error(w, "bad request", 400)
				return
			}
			secret := nativeKey
			if in.Key == "internal" {
				secret = "fixture-internal-agent-key-abcdef"
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"secret": secret})
		default:
			http.NotFound(w, r)
		}
	}))
	defer web.Close()
	webURL, _ := url.Parse(web.URL)
	port, _ := strconv.Atoi(webURL.Port())
	expectedCookie, expectedOrigin = "magpie_web_"+strconv.Itoa(port)+"=fixture-web-key", web.URL
	p := &nativeProcess{
		webURL: web.URL, webHost: webURL.Host, webKey: "fixture-web-key", ports: nativePorts{Web: port},
		gateway: backend.URL, client: web.Client(),
	}
	h := p.GatewayHandler()
	for name, authorization := range map[string]string{
		"missing": "", "wrong": "Bearer invalid-public-negative", "synthetic OMP": "Bearer magpie-omp",
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://workbench.test/v1/models", nil)
			if authorization != "" {
				r.Header.Set("Authorization", authorization)
			}
			r.AddCookie(&http.Cookie{Name: "magpie_web_12345", Value: "fixture-web-key"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", w.Code)
			}
		})
	}
	if backendCalls != 0 {
		t.Fatalf("invalid credentials reached gateway: %d calls", backendCalls)
	}
	r := httptest.NewRequest(http.MethodGet, "http://workbench.test/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+nativeKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || backendCalls != 1 {
		t.Fatalf("valid native key status=%d backend calls=%d", w.Code, backendCalls)
	}

	// The socket address exposed to the child is loopback; the caller key must
	// still be forwarded so Magpie applies its native model, quota and usage policy.
	if got := backendCalls; got != 1 {
		t.Fatalf("unexpected gateway calls: %d", got)
	}
	// A public caller is another machine's: the child is told so, and only
	// sharing plus an enabled gateway key answer it.
	if forwarded != "remote-workbench-client" {
		t.Fatalf("public caller forwarded as %q, want the remote mark", forwarded)
	}
	// This computer's own agents are not: they keep reaching their Magpie while
	// the gateway is not shared.
	forwarded = "unset"
	r = httptest.NewRequest(http.MethodGet, "http://workbench.test/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+nativeKey)
	w = httptest.NewRecorder()
	p.AgentGatewayHandler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || forwarded != "" || backendCalls != 2 {
		t.Fatalf("agent entry status=%d forwarded=%q calls=%d", w.Code, forwarded, backendCalls)
	}
}
