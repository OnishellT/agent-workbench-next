package hosts

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type hostAgentProxyObservation struct {
	Method        string
	RequestURI    string
	Host          string
	Origin        string
	Cookie        string
	Authorization string
	Forwarded     string
	Body          string
}

func TestHostAgentProxyReplacesRootCredentialsAndPreservesInferenceCallerKey(t *testing.T) {
	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	token := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	var mu sync.Mutex
	var observations []hostAgentProxyObservation
	authCalls := 0
	var helper *httptest.Server
	helper = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"application": "agent-workbench", "modules": map[string]string{"settings": "mounted", "models": "mounted"}})
		case "/api/auth":
			data, _ := io.ReadAll(r.Body)
			var request struct {
				Token string `json:"token"`
			}
			mu.Lock()
			authCalls++
			mu.Unlock()
			if string(data) != `{"token":"`+token+`"}` || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Origin") != helper.URL || r.Host != strings.TrimPrefix(helper.URL, "http://") {
				http.Error(w, "bad internal auth request", http.StatusUnauthorized)
				return
			}
			if err := json.Unmarshal(data, &request); err != nil || request.Token != token {
				http.Error(w, "bad token", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "workbench_session_proxytest", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/api/models/echo", "/v1/models":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			observations = append(observations, hostAgentProxyObservation{Method: r.Method, RequestURI: r.URL.RequestURI(), Host: r.Host, Origin: r.Header.Get("Origin"), Cookie: r.Header.Get("Cookie"), Authorization: r.Header.Get("Authorization"), Forwarded: r.Header.Get("X-Forwarded-For"), Body: string(body)})
			mu.Unlock()
			if r.URL.Path == "/api/models/echo" {
				http.SetCookie(w, &http.Cookie{Name: "workbench_session_leak", Value: token, Path: "/", HttpOnly: true})
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, "accepted")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[]}`)
		case "/api/models/redirect":
			w.Header().Set("Location", helper.URL+"/private")
			http.Redirect(w, r, helper.URL+"/private", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer helper.Close()

	m, _ := newTestModule(t)
	defer m.Close()
	host := trustedHostAgentFixture(t, m, address, port, pin)
	stateDir := filepath.Join(home, ".local", "state", "agent-workbench-next", "host-agent", host.ID)
	if err := writeReadyHostAgentFixture(stateDir, host.ID, os.Geteuid(), helper.Listener.Addr().(*net.TCPAddr)); err != nil {
		t.Fatal(err)
	}
	handler := m.HostsHandler()
	digestsBefore := hostAgentFixtureDigestRuns.Load()

	echoRequest := httptest.NewRequest(http.MethodPost, "http://root.example/api/hosts/fixture-agent/models/echo?job=7", strings.NewReader("models-body"))
	echoRequest.Header.Set("Cookie", "workbench_session_root=root-cookie-secret")
	echoRequest.Header.Set("Authorization", "Bearer root-header-secret")
	echoRequest.Header.Set("Origin", "https://root.example")
	echoRequest.Header.Set("X-Forwarded-For", "192.0.2.55")
	echoResponse := httptest.NewRecorder()
	handler.ServeHTTP(echoResponse, echoRequest)
	if echoResponse.Code != http.StatusCreated || echoResponse.Body.String() != "accepted" || len(echoResponse.Header().Values("Set-Cookie")) != 0 || strings.Contains(echoResponse.Body.String(), token) {
		t.Fatalf("models proxy response = %d, headers=%v, body=%q", echoResponse.Code, echoResponse.Header(), echoResponse.Body.String())
	}

	modelRequest := httptest.NewRequest(http.MethodGet, "http://root.example/api/hosts/fixture-agent/models/v1/models?alt=sse", nil)
	modelRequest.Header.Set("Cookie", "workbench_session_root=root-cookie-secret")
	modelRequest.Header.Set("Authorization", "Bearer caller-model-key-123")
	modelRequest.Header.Set("Origin", "https://root.example")
	modelResponse := httptest.NewRecorder()
	handler.ServeHTTP(modelResponse, modelRequest)
	if modelResponse.Code != http.StatusOK || modelResponse.Body.String() != `{"data":[]}` || len(modelResponse.Header().Values("Set-Cookie")) != 0 || strings.Contains(modelResponse.Body.String(), token) {
		t.Fatalf("models inference proxy response = %d, headers=%v, body=%q", modelResponse.Code, modelResponse.Header(), modelResponse.Body.String())
	}

	redirectRequest := httptest.NewRequest(http.MethodGet, "http://root.example/api/hosts/fixture-agent/models/redirect", nil)
	redirectRequest.Header.Set("Cookie", "workbench_session_root=root-cookie-secret")
	redirectResponse := httptest.NewRecorder()
	handler.ServeHTTP(redirectResponse, redirectRequest)
	if redirectResponse.Code != http.StatusBadGateway || redirectResponse.Header().Get("Location") != "" || len(redirectResponse.Header().Values("Set-Cookie")) != 0 || strings.Contains(redirectResponse.Body.String(), helper.URL) || strings.Contains(redirectResponse.Body.String(), token) {
		t.Fatalf("private helper redirect escaped the proxy: %d headers=%v body=%q", redirectResponse.Code, redirectResponse.Header(), redirectResponse.Body.String())
	}
	if runs := hostAgentFixtureDigestRuns.Load() - digestsBefore; runs != 1 {
		t.Fatalf("remote binary digest ran %d times for 3 proxied requests; the inspection must be cached", runs)
	}
	mu.Lock()
	defer mu.Unlock()
	if authCalls != 3 {
		t.Fatalf("helper auth calls = %d, want one per root request", authCalls)
	}
	if len(observations) != 2 {
		t.Fatalf("helper requests = %d, want 2", len(observations))
	}
	echo := observations[0]
	if echo.Method != http.MethodPost || echo.RequestURI != "/api/models/echo?job=7" || echo.Host != strings.TrimPrefix(helper.URL, "http://") || echo.Origin != helper.URL || echo.Cookie != "workbench_session_proxytest="+token || echo.Authorization != "" || echo.Forwarded != "" || echo.Body != "models-body" {
		t.Fatalf("models request crossed the tunnel with unsafe or changed fields: %+v", echo)
	}
	model := observations[1]
	if model.Method != http.MethodGet || model.RequestURI != "/v1/models?alt=sse" || model.Host != strings.TrimPrefix(helper.URL, "http://") || model.Origin != helper.URL || model.Cookie != "workbench_session_proxytest="+token || model.Authorization != "Bearer caller-model-key-123" || model.Forwarded != "" {
		t.Fatalf("inference request did not preserve only the caller key across the private helper session: %+v", model)
	}
}

func TestHostAgentProxyRejectsRootTokenAndDoesNotFallbackWhenHelperIsAbsent(t *testing.T) {
	m, _ := newTestModule(t)
	defer m.Close()
	handler := m.HostsHandler()
	rootTokenRequest := httptest.NewRequest(http.MethodGet, "http://root.example/api/hosts/missing/models/v1/models", nil)
	rootTokenRequest.Header.Set("Cookie", "workbench_session_root=root-cookie-secret")
	rootTokenRequest.Header.Set("Authorization", "Bearer root-cookie-secret")
	rootTokenResponse := httptest.NewRecorder()
	handler.ServeHTTP(rootTokenResponse, rootTokenRequest)
	if rootTokenResponse.Code != http.StatusUnauthorized {
		t.Fatalf("root session bearer was not rejected as a caller inference key: %d %s", rootTokenResponse.Code, rootTokenResponse.Body.String())
	}

	home := t.TempDir()
	address, port, pin, _ := hostAgentSSHFixture(t, home)
	host := trustedHostAgentFixture(t, m, address, port, pin)
	// Work and its terminal are retired: their host routes stay unregistered and
	// answer 404 without touching any helper or local fallback. The models
	// surface below still fails closed when the remote helper is absent.
	for _, retired := range []string{"/work/status", "/work", "/terminal/stream", "/terminal"} {
		request := httptest.NewRequest(http.MethodGet, "http://root.example/api/hosts/"+host.ID+retired, nil)
		request.Header.Set("Cookie", "workbench_session_root=root-cookie-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || strings.Contains(strings.ToLower(response.Body.String()), "local") {
			t.Fatalf("retired %s = %d %s, want 404 without a local fallback", retired, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://root.example/api/hosts/"+host.ID+"/models/state", nil)
	request.Header.Set("Cookie", "workbench_session_root=root-cookie-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(strings.ToLower(response.Body.String()), "local") {
		t.Fatalf("absent remote helper must fail closed without a local fallback: %d %s", response.Code, response.Body.String())
	}
}
