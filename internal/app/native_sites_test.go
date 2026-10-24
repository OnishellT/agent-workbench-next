package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/config"
)

func nativeAppForTest(t *testing.T, sites ...NativeSite) (*Server, config.Config) {
	t.Helper()
	cfg := testConfig(t)
	handler, err := New(cfg, testAssets(), testModules(t, cfg, Modules{NativeSites: sites}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close(t.Context()) })
	return handler, cfg
}

func TestNativeSiteGrantFragmentExchangeAndLogoutRevocation(t *testing.T) {
	const origin = "https://magpie.example:8443"
	opened := false
	underlying := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opened = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("original magpie UI"))
	})
	site := NativeSite{HostID: "local", Listen: "127.0.0.1:39001", Origin: origin, Handler: underlying}
	other := NativeSite{HostID: "other", Listen: "127.0.0.1:39002", Origin: "https://other.example", Handler: underlying}
	server, cfg := nativeAppForTest(t, site, other)
	wb := authorize(t, server.Handler(), cfg)
	open := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/local/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader("{\"view\":\"agents\"}"), cfg.PublicURL)
	open.Header.Set("Content-Type", "application/json")
	addCookie(open, wb)
	response := serve(server.Handler(), open)
	if response.Code != http.StatusOK {
		t.Fatalf("open status=%d body=%s", response.Code, response.Body)
	}
	var result struct {
		URL    string `json:"url"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(result.URL)
	if err != nil || result.Origin != origin || u.Scheme != "https" || u.Host != "magpie.example:8443" || u.Path != nativeEnterPath || u.RawQuery != "" || len(u.Fragment) != 43 {
		t.Fatalf("open URL leaked a grant in path/query or lost public origin: %#v %v", result, err)
	}

	portal := server.NativeSiteHandler("local")
	entry := newRequest(http.MethodGet, origin+nativeEnterPath, u.Host, nil, "")
	entryResponse := serve(portal, entry)
	if entryResponse.Code != http.StatusOK || strings.Contains(entryResponse.Body.String(), u.Fragment) || entryResponse.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("fragment bootstrap response status=%d body=%q headers=%v", entryResponse.Code, entryResponse.Body.String(), entryResponse.Header())
	}
	consume := func(handler http.Handler, host, requestOrigin, grant string) *httptest.ResponseRecorder {
		r := newRequest(http.MethodPost, requestOrigin+nativeGrantPath, host, strings.NewReader("{\"grant\":\""+grant+"\"}"), requestOrigin)
		r.Header.Set("Content-Type", "application/json")
		return serve(handler, r)
	}
	consumed := consume(portal, u.Host, origin, u.Fragment)
	if consumed.Code != http.StatusOK || len(consumed.Result().Cookies()) != 1 {
		t.Fatalf("consume status=%d body=%s", consumed.Code, consumed.Body)
	}
	portalCookie := consumed.Result().Cookies()[0]
	if !portalCookie.HttpOnly || portalCookie.Name != nativePortalCookie("local", server.cookieName) || portalCookie.Name == nativePortalCookie("local", instanceCookieName(strings.Repeat("z", 48))) {
		t.Fatalf("portal cookie is not instance-scoped and HttpOnly: %#v", portalCookie)
	}
	page := newRequest(http.MethodGet, origin+"/?view=agents", u.Host, nil, "")
	addCookie(page, portalCookie)
	if got := serve(portal, page); got.Code != http.StatusOK || !opened {
		t.Fatalf("authorized native page status=%d", got.Code)
	}
	if got := consume(portal, u.Host, origin, u.Fragment); got.Code != http.StatusForbidden {
		t.Fatalf("grant replay status=%d, want 403", got.Code)
	}

	openOther := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/other/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader("{}"), cfg.PublicURL)
	openOther.Header.Set("Content-Type", "application/json")
	addCookie(openOther, wb)
	otherResponse := serve(server.Handler(), openOther)
	var otherResult struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(otherResponse.Body.Bytes(), &otherResult); err != nil {
		t.Fatal(err)
	}
	otherURL, _ := url.Parse(otherResult.URL)
	otherOrigin := otherURL.Scheme + "://" + otherURL.Host
	if got := consume(portal, u.Host, origin, otherURL.Fragment); got.Code != http.StatusForbidden {
		t.Fatalf("cross-host grant status=%d, want 403", got.Code)
	}
	if got := consume(server.NativeSiteHandler("other"), otherURL.Host, otherOrigin, otherURL.Fragment); got.Code != http.StatusForbidden {
		t.Fatalf("cross-host grant replay status=%d, want 403", got.Code)
	}

	logout := newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	addCookie(logout, wb)
	if got := serve(server.Handler(), logout); got.Code != http.StatusOK {
		t.Fatalf("logout status=%d", got.Code)
	}
	page = newRequest(http.MethodGet, origin+"/", u.Host, nil, "")
	addCookie(page, portalCookie)
	if got := serve(portal, page); got.Code != http.StatusUnauthorized {
		t.Fatalf("portal remained valid after Workbench logout: status=%d", got.Code)
	}
}

func TestNativeSiteOriginDoesNotNeedPrivateListenerPort(t *testing.T) {
	for _, origin := range []string{"https://magpie.example", "https://127.0.0.1:18904", "http://127.0.0.1:39003"} {
		site := NativeSite{HostID: "test", Listen: "127.0.0.1:39004", Origin: origin, Handler: http.NotFoundHandler()}
		if err := validateNativeSite(site); err != nil {
			t.Errorf("valid independent origin %q rejected: %v", origin, err)
		}
	}
	if err := validateNativeSite(NativeSite{HostID: "test", Listen: "0.0.0.0:39004", Origin: "https://magpie.example", Handler: http.NotFoundHandler()}); err == nil {
		t.Fatal("non-loopback portal listener accepted")
	}
}

func TestReadNativeViewMatchesMagpieOpenViews(t *testing.T) {
	for _, view := range []string{"", "agents", "providers", "routing", "gateway", "plugins", "settings", "usage", "sessions", "library", "custom-plugin_2"} {
		body := strings.NewReader("{\"view\":\"" + view + "\"}")
		got, err := readNativeView(body)
		want := view
		if want == "" {
			want = "agents"
		}
		if err != nil || got != want {
			t.Errorf("readNativeView(%q) = %q, %v; want %q", view, got, err, want)
		}
	}
	for _, view := range []string{"../settings", "plugin?x=1", "two words", strings.Repeat("a", 81)} {
		body := strings.NewReader("{\"view\":\"" + view + "\"}")
		if got, err := readNativeView(body); err == nil {
			t.Errorf("readNativeView(%q) = %q, want rejection", view, got)
		}
	}
}

func TestDynamicRemoteNativeSiteReusesSharedHostScopedPortal(t *testing.T) {
	cfg := testConfig(t)
	const localOrigin = "http://magpie.localhost:39021"
	const remoteOrigin = "http://h-a17b.magpie.localhost:39021"
	localOpened, remoteOpened := false, false
	local := NativeSite{HostID: "local", Listen: "127.0.0.1:39021", Origin: localOrigin, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { localOpened = true; w.WriteHeader(http.StatusOK) })}
	remote := NativeSite{HostID: "remote-host", Listen: local.Listen, Origin: remoteOrigin, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { remoteOpened = true; w.WriteHeader(http.StatusOK) })}
	modules := testModules(t, cfg, Modules{NativeSites: []NativeSite{local}, NativeSiteResolver: func(_ context.Context, hostID string) (NativeSite, error) {
		if hostID == remote.HostID {
			return remote, nil
		}
		return NativeSite{}, nil
	}})
	server, err := New(cfg, testAssets(), modules)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(t.Context()) })
	wb := authorize(t, server.Handler(), cfg)
	open := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/remote-host/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{"view":"providers"}`), cfg.PublicURL)
	open.Header.Set("Content-Type", "application/json")
	addCookie(open, wb)
	response := serve(server.Handler(), open)
	if response.Code != http.StatusOK {
		t.Fatalf("remote open status=%d body=%s", response.Code, response.Body)
	}
	var result struct {
		URL    string `json:"url"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Origin != remoteOrigin {
		t.Fatalf("remote resolver result=%+v err=%v", result, err)
	}
	u, err := url.Parse(result.URL)
	if err != nil || u.Host != "h-a17b.magpie.localhost:39021" || u.Fragment == "" {
		t.Fatalf("remote origin/grant not host scoped: %q (%v)", result.URL, err)
	}
	portal := server.NativeSiteHandler("")
	entry := newRequest(http.MethodGet, remoteOrigin+nativeEnterPath, u.Host, nil, "")
	if got := serve(portal, entry); got.Code != http.StatusOK {
		t.Fatalf("dynamic entry status=%d body=%s", got.Code, got.Body)
	}
	consume := newRequest(http.MethodPost, remoteOrigin+nativeGrantPath, u.Host, strings.NewReader(`{"grant":"`+u.Fragment+`"}`), remoteOrigin)
	consume.Header.Set("Content-Type", "application/json")
	consumed := serve(portal, consume)
	if consumed.Code != http.StatusOK || len(consumed.Result().Cookies()) != 1 {
		t.Fatalf("dynamic grant consume status=%d body=%s", consumed.Code, consumed.Body)
	}
	page := newRequest(http.MethodGet, remoteOrigin+"/?view=providers", u.Host, nil, "")
	addCookie(page, consumed.Result().Cookies()[0])
	if got := serve(portal, page); got.Code != http.StatusOK || !remoteOpened || localOpened {
		t.Fatalf("shared site dispatch local=%v remote=%v status=%d", localOpened, remoteOpened, got.Code)
	}
	if got := len(server.NativeSiteListeners()); got != 1 {
		t.Fatalf("shared local listener count=%d, want 1", got)
	}
	missing := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/unavailable/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{}`), cfg.PublicURL)
	missing.Header.Set("Content-Type", "application/json")
	addCookie(missing, wb)
	if got := serve(server.Handler(), missing); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing remote Magpie status=%d, want 503", got.Code)
	}
	logout := newRequest(http.MethodPost, cfg.PublicURL+"/api/logout", mustURLHost(t, cfg.PublicURL), nil, cfg.PublicURL)
	addCookie(logout, wb)
	if got := serve(server.Handler(), logout); got.Code != http.StatusOK {
		t.Fatalf("logout status=%d", got.Code)
	}
	page = newRequest(http.MethodGet, remoteOrigin+"/", u.Host, nil, "")
	addCookie(page, consumed.Result().Cookies()[0])
	if got := serve(portal, page); got.Code != http.StatusUnauthorized {
		t.Fatalf("remote native session survived logout: status=%d", got.Code)
	}
}

// Several openers asking for the same host at once resolve it once: one site per host.
func TestConcurrentOpenersResolveOneSitePerHost(t *testing.T) {
	cfg := testConfig(t)
	var mu sync.Mutex
	calls := map[string]int{}
	total := 0
	started := make(chan struct{})
	resolver := func(_ context.Context, hostID string) (NativeSite, error) {
		mu.Lock()
		calls[hostID]++
		total++
		count := total
		mu.Unlock()
		if hostID == "remote-host" && count == 1 {
			close(started)
			time.Sleep(150 * time.Millisecond) // the other openers pile up while this one works
		}
		address := fmt.Sprintf("127.0.0.1:%d", 39061+count)
		return NativeSite{HostID: hostID, Listen: address, Origin: "http://" + address, Handler: http.NotFoundHandler()}, nil
	}
	local := NativeSite{HostID: "local", Listen: "127.0.0.1:39060", Origin: "http://127.0.0.1:39060", Handler: http.NotFoundHandler()}
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{NativeSites: []NativeSite{local}, NativeSiteResolver: resolver}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(t.Context()) })
	wb := authorize(t, server.Handler(), cfg)
	open := func(hostID string) (int, string) {
		request := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/"+hostID+"/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{"view":"agents"}`), cfg.PublicURL)
		request.Header.Set("Content-Type", "application/json")
		addCookie(request, wb)
		response := serve(server.Handler(), request)
		var body struct {
			Origin string `json:"origin"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response.Code, body.Origin
	}
	const openers = 8
	codes, origins := make([]int, openers), make([]string, openers)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); codes[0], origins[0] = open("remote-host") }()
	<-started
	for i := 1; i < openers; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); codes[i], origins[i] = open("remote-host") }(i)
	}
	wg.Wait()
	for i := range codes {
		if codes[i] != http.StatusOK || origins[i] != origins[0] {
			t.Fatalf("opener %d got %d %q, want 200 %q", i, codes[i], origins[i], origins[0])
		}
	}
	mu.Lock()
	resolved := calls["remote-host"]
	mu.Unlock()
	if resolved != 1 {
		t.Fatalf("the same host resolved %d times, want 1", resolved)
	}
	// Another host still resolves on its own, and a later opener reuses the site cached.
	if code, other := open("other-host"); code != http.StatusOK || other == origins[0] {
		t.Fatalf("second host got %d %q, want 200 and its own origin", code, other)
	}
	if code, again := open("remote-host"); code != http.StatusOK || again != origins[0] {
		t.Fatalf("cached host got %d %q, want 200 %q", code, again, origins[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["remote-host"] != 1 || calls["other-host"] != 1 {
		t.Fatalf("resolutions per host = %v, want one each", calls)
	}
}

// A loopback installation serves each host on its own port: the portal dispatches and
// authorizes by the origin that really serves the panel, never handing one host's portal
// to another.
func TestDynamicRemoteNativeSiteOnItsOwnLoopbackListener(t *testing.T) {
	cfg := testConfig(t)
	const localOrigin = "http://127.0.0.1:39031"
	const remoteOrigin = "http://127.0.0.1:39032"
	localOpened, remoteOpened := false, false
	local := NativeSite{HostID: "local", Listen: "127.0.0.1:39031", Origin: localOrigin, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { localOpened = true; w.WriteHeader(http.StatusOK) })}
	remote := NativeSite{HostID: "remote-host", Listen: "127.0.0.1:39032", Origin: remoteOrigin, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { remoteOpened = true; w.WriteHeader(http.StatusOK) })}
	modules := testModules(t, cfg, Modules{NativeSites: []NativeSite{local}, NativeSiteResolver: func(_ context.Context, hostID string) (NativeSite, error) {
		if hostID == remote.HostID {
			return remote, nil
		}
		return NativeSite{}, nil
	}})
	server, err := New(cfg, testAssets(), modules)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(t.Context()) })
	wb := authorize(t, server.Handler(), cfg)
	open := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/remote-host/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{"view":"providers"}`), cfg.PublicURL)
	open.Header.Set("Content-Type", "application/json")
	addCookie(open, wb)
	response := serve(server.Handler(), open)
	if response.Code != http.StatusOK {
		t.Fatalf("remote open status=%d body=%s", response.Code, response.Body)
	}
	var result struct {
		URL    string `json:"url"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Origin != remoteOrigin {
		t.Fatalf("remote resolver result=%+v err=%v", result, err)
	}
	u, err := url.Parse(result.URL)
	if err != nil || u.Host != "127.0.0.1:39032" || u.Fragment == "" {
		t.Fatalf("remote origin/grant not host scoped: %q (%v)", result.URL, err)
	}
	portal := server.NativeSiteHandler("")
	entry := newRequest(http.MethodGet, remoteOrigin+nativeEnterPath, u.Host, nil, "")
	if got := serve(portal, entry); got.Code != http.StatusOK {
		t.Fatalf("dynamic entry status=%d body=%s", got.Code, got.Body)
	}
	consume := newRequest(http.MethodPost, remoteOrigin+nativeGrantPath, u.Host, strings.NewReader(`{"grant":"`+u.Fragment+`"}`), remoteOrigin)
	consume.Header.Set("Content-Type", "application/json")
	consumed := serve(portal, consume)
	if consumed.Code != http.StatusOK || len(consumed.Result().Cookies()) != 1 {
		t.Fatalf("dynamic grant consume status=%d body=%s", consumed.Code, consumed.Body)
	}
	page := newRequest(http.MethodGet, remoteOrigin+"/?view=providers", u.Host, nil, "")
	addCookie(page, consumed.Result().Cookies()[0])
	if got := serve(portal, page); got.Code != http.StatusOK || !remoteOpened || localOpened {
		t.Fatalf("per-port site dispatch local=%v remote=%v status=%d", localOpened, remoteOpened, got.Code)
	}
	// The portal of one host is not the portal of the other, even on the same address.
	other := newRequest(http.MethodGet, localOrigin+"/", mustURLHost(t, localOrigin), nil, "")
	addCookie(other, consumed.Result().Cookies()[0])
	if got := serve(portal, other); got.Code != http.StatusUnauthorized || localOpened {
		t.Fatalf("host portal opened another site: status=%d local=%v", got.Code, localOpened)
	}
}

// The page's own files must be served by their token and named by this host's public origin.
func TestNativePluginPagePolicyIsAnchoredToThisInstallation(t *testing.T) {
	const token = "5c5c2b7ad7cc95828ad6c28574b0e399"
	const own = "127.0.0.1:19033/_magpie/plugin/" + token + "/"
	const pluginPath = "/_magpie/plugin/" + token + "/page.html"
	childPolicy := "sandbox allow-scripts; default-src 'none'; script-src " + own + "; style-src " + own + " 'unsafe-inline'; img-src " + own + " data:; connect-src 'none'; frame-ancestors 'self'"
	for _, tt := range []struct{ name, siteOrigin, rootOrigin string }{
		{"the panel on loopback HTTP", "http://127.0.0.1:19031", "http://127.0.0.1:19030"},
		{"the panel behind HTTPS", "https://magpie.example:8443", "https://workbench.example:8443"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			site := NativeSite{HostID: "local", Listen: "127.0.0.1:39001", Origin: tt.siteOrigin, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Security-Policy", childPolicy)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte("<!doctype html><title>a plugin's page</title>"))
			})}
			cfg := testConfig(t)
			cfg.Listen = "127.0.0.1:19030"
			cfg.PublicURL = tt.rootOrigin
			server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{NativeSites: []NativeSite{site}}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close(t.Context()) })
			portal := server.NativeSiteHandler("local")

			// the panel's own grant and cookie, exactly as the window gets them
			wb := authorize(t, server.Handler(), cfg)
			open := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/local/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader(`{"view":"agents"}`), cfg.PublicURL)
			open.Header.Set("Content-Type", "application/json")
			addCookie(open, wb)
			opened := serve(server.Handler(), open)
			var grant struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(opened.Body.Bytes(), &grant); err != nil || opened.Code != http.StatusOK {
				t.Fatalf("open status=%d body=%s err=%v", opened.Code, opened.Body, err)
			}
			u, err := url.Parse(grant.URL)
			if err != nil {
				t.Fatal(err)
			}
			consume := newRequest(http.MethodPost, tt.siteOrigin+nativeGrantPath, u.Host, strings.NewReader(`{"grant":"`+u.Fragment+`"}`), tt.siteOrigin)
			consume.Header.Set("Content-Type", "application/json")
			consumed := serve(portal, consume)
			if consumed.Code != http.StatusOK || len(consumed.Result().Cookies()) != 1 {
				t.Fatalf("consume status=%d body=%s", consumed.Code, consumed.Body)
			}
			portalCookie := consumed.Result().Cookies()[0]

			page := newRequest(http.MethodGet, tt.siteOrigin+pluginPath, u.Host, nil, "")
			addCookie(page, portalCookie)
			got := serve(portal, page)
			if got.Code != http.StatusOK {
				t.Fatalf("plugin page status=%d body=%s", got.Code, got.Body)
			}
			policy := got.Header().Get("Content-Security-Policy")
			publicOwn := mustURLHost(t, tt.siteOrigin) + "/_magpie/plugin/" + token + "/"
			for _, want := range []string{
				"script-src " + publicOwn,
				"style-src " + publicOwn + " 'unsafe-inline'",
				"img-src " + publicOwn + " data:",
				"sandbox allow-scripts",
				"default-src 'none'",
				"connect-src 'none'",
				"frame-ancestors 'self' " + tt.rootOrigin,
			} {
				if !strings.Contains(policy, want) {
					t.Errorf("policy %q does not hold %q", policy, want)
				}
			}
			for _, unwanted := range []string{"127.0.0.1:19033", "allow-same-origin", "unsafe-eval", "https://evil.example", "*"} {
				if strings.Contains(policy, unwanted) {
					t.Errorf("policy %q was widened with %q", policy, unwanted)
				}
			}
			if frames := policyDirective(policy, "frame-ancestors"); frames != "'self' "+tt.rootOrigin {
				t.Errorf("frame-ancestors = %q, want only the page's own origin and this installation's", frames)
			}
			if other := policyDirective(policy, "script-src"); other != publicOwn {
				t.Errorf("script-src = %q, want the page's own folder alone", other)
			}

			// the page's own frame sends no cookie; every other path still needs it
			for _, tt2 := range []struct {
				name, path, method string
				want               int
			}{
				{"the page itself", pluginPath, http.MethodGet, http.StatusOK},
				{"the page's own bridge", "/_magpie/plugin/" + token + "/_magpie/bridge.js", http.MethodGet, http.StatusOK},
				{"another path of the app", "/api/plugins", http.MethodGet, http.StatusUnauthorized},
				{"dot segments out of the page folder", "/_magpie/plugin/" + token + "/../../api/plugins", http.MethodGet, http.StatusBadRequest},
				{"encoded dot segments", "/_magpie/plugin/" + token + "/%2e%2e/%2e%2e/api/plugins", http.MethodGet, http.StatusBadRequest},
				{"the window itself", "/", http.MethodGet, http.StatusUnauthorized},
			} {
				withoutCookie := newRequest(tt2.method, tt.siteOrigin+tt2.path, u.Host, nil, "")
				if got := serve(portal, withoutCookie); got.Code != tt2.want {
					t.Errorf("%s with no cookie: status=%d, want %d", tt2.name, got.Code, tt2.want)
				}
			}
			for _, tt2 := range []struct {
				name, path, method string
				want               int
			}{
				{"another token's page", "/_magpie/plugin/" + strings.Repeat("0", 32) + "/page.html", http.MethodGet, http.StatusOK},
				{"a path that is no page", "/_magpie/plugins", http.MethodGet, http.StatusUnauthorized},
			} {
				withoutCookie := newRequest(tt2.method, tt.siteOrigin+tt2.path, u.Host, nil, "")
				if got := serve(portal, withoutCookie); got.Code != tt2.want {
					t.Errorf("%s with no cookie: status=%d, want %d", tt2.name, got.Code, tt2.want)
				}
			}
			// another site's window is refused before any policy is handed out
			foreign := newRequest(http.MethodGet, tt.siteOrigin+pluginPath, u.Host, nil, "https://evil.example")
			addCookie(foreign, portalCookie)
			if got := serve(portal, foreign); got.Code != http.StatusForbidden {
				t.Errorf("foreign-origin plugin page status=%d, want 403", got.Code)
			}
			// a path that is not a plugin page's file keeps the child's policy
			otherPath := newRequest(http.MethodGet, tt.siteOrigin+"/api/plugins", u.Host, nil, "")
			addCookie(otherPath, portalCookie)
			if got := serve(portal, otherPath); got.Code != http.StatusOK || got.Header().Get("Content-Security-Policy") != childPolicy {
				t.Errorf("non-plugin path policy=%q status=%d, want the child's own", got.Header().Get("Content-Security-Policy"), got.Code)
			}
		})
	}
}

// policyDirective is one directive's value out of a policy, for the assertions
// above: the child's policy is a list of `name value` pairs.
func policyDirective(policy, name string) string {
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if directive, value, ok := strings.Cut(part, " "); ok && directive == name {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func TestNativePluginPagePolicyTouchesOnlyThePagesOwnFolder(t *testing.T) {
	const token = "5c5c2b7ad7cc95828ad6c28574b0e399"
	own := "/_magpie/plugin/" + token + "/"
	private := "127.0.0.1:19033" + own
	policy := "sandbox allow-scripts; default-src 'none'; script-src " + private + " 'self'; worker-src " + private + "; img-src " + private + " data:; connect-src 'none'; frame-ancestors 'self'; report-uri https://reports.example/collect"
	got := nativePluginPagePolicy(policy, own, "magpie.example:8443", "https://workbench.example:8443")
	want := "sandbox allow-scripts; default-src 'none'; script-src magpie.example:8443" + own + " 'self'; worker-src magpie.example:8443" + own + "; img-src magpie.example:8443" + own + " data:; connect-src 'none'; frame-ancestors 'self' https://workbench.example:8443; report-uri https://reports.example/collect"
	if got != want {
		t.Errorf("translated policy = %q, want %q", got, want)
	}
	// the page's folder alone moves: another host, and a token that is not the
	// page's own, are the child's to name
	other := "script-src https://cdn.example/x " + private
	if got, want := nativePluginPagePolicy(other, own, "magpie.example:8443", "https://workbench.example:8443"), "script-src https://cdn.example/x magpie.example:8443"+own; got != want {
		t.Errorf("other-host policy = %q, want %q", got, want)
	}
	// a frame rule the child already widened is left for the child, not
	// rewritten into this installation's
	frames := "frame-ancestors 'self' https://third.example"
	if got := nativePluginPagePolicy(frames, own, "magpie.example:8443", "https://workbench.example:8443"); got != frames {
		t.Errorf("frame-ancestors rewritten to %q, want %q", got, frames)
	}
	// with no configured origin to add, the page keeps the frames it had
	if got := nativePluginPagePolicy(frames, own, "magpie.example:8443", ""); got != frames {
		t.Errorf("frame-ancestors without a root = %q, want %q", got, frames)
	}
	for _, path := range []string{"/_magpie/plugin/" + token + "/page.html", "/_magpie/plugin/" + token + "/_magpie/bridge.js"} {
		if own, ok := nativePluginPagePath(path); !ok || own != "/_magpie/plugin/"+token+"/" {
			t.Errorf("nativePluginPagePath(%q) = %q, %v", path, own, ok)
		}
	}
	for _, path := range []string{
		"/api/plugins",
		"/_magpie/plugin/" + token,
		"/_magpie/plugin/" + strings.ToUpper(token) + "/page.html",
		"/_magpie/plugin/" + token[:31] + "/page.html",
		"/_magpie/plugin/" + token + "0/page.html",
		"/_magpie/pluginfoo/" + token + "/page.html",
	} {
		if own, ok := nativePluginPagePath(path); ok {
			t.Errorf("nativePluginPagePath(%q) = %q, want no plugin page", path, own)
		}
	}
}

func TestCloseEndsInFlightNativeSiteStreams(t *testing.T) {
	const origin = "https://magpie.example:8443"
	started := make(chan struct{})
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			w.WriteHeader(http.StatusOK)
			return
		}
		close(started)
		<-r.Context().Done() // an SSE stream of the framed Magpie: it ends only when cancelled
	})
	site := NativeSite{HostID: "local", Listen: "127.0.0.1:39001", Origin: origin, Handler: stream}
	server, cfg := nativeAppForTest(t, site)
	wb := authorize(t, server.Handler(), cfg)
	open := newRequest(http.MethodPost, cfg.PublicURL+"/api/hosts/local/magpie/open", mustURLHost(t, cfg.PublicURL), strings.NewReader("{}"), cfg.PublicURL)
	open.Header.Set("Content-Type", "application/json")
	addCookie(open, wb)
	var opened struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(serve(server.Handler(), open).Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(opened.URL)
	if err != nil {
		t.Fatal(err)
	}
	portal := server.NativeSiteHandler("local")
	grant := newRequest(http.MethodPost, origin+nativeGrantPath, u.Host, strings.NewReader("{\"grant\":\""+u.Fragment+"\"}"), origin)
	grant.Header.Set("Content-Type", "application/json")
	consumed := serve(portal, grant)
	if consumed.Code != http.StatusOK || len(consumed.Result().Cookies()) != 1 {
		t.Fatalf("consume status=%d", consumed.Code)
	}
	portalCookie := consumed.Result().Cookies()[0]

	done := make(chan struct{})
	go func() {
		defer close(done)
		r := newRequest(http.MethodGet, origin+"/stream", u.Host, nil, "")
		addCookie(r, portalCookie)
		serve(portal, r)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream never reached the framed Magpie")
	}
	closed := make(chan error, 1)
	go func() { closed <- server.Close(t.Context()) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close left a native-site stream running, so Shutdown would wait for it")
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
	late := newRequest(http.MethodGet, origin+"/", u.Host, nil, "")
	addCookie(late, portalCookie)
	if got := serve(portal, late); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("a native-site request after Close = %d, want 503", got.Code)
	}
}
