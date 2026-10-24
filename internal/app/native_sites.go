package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"workbench/internal/config"
)

const (
	nativeGrantTTL   = 45 * time.Second
	nativePortalTTL  = 12 * time.Hour
	nativeOpenLimit  = 4096
	nativeSiteCookie = "workbench_magpie_"
	nativeEnterPath  = "/_workbench/magpie/enter"
	nativeEnterJS    = "/_workbench/magpie/enter.js"
	nativeGrantPath  = "/_workbench/magpie/consume"
)

var nativeViewPattern = regexp.MustCompile("^[A-Za-z0-9_-]{1,80}$")

type NativeSite struct {
	HostID  string
	Listen  string
	Origin  string
	Handler http.Handler
}

type nativeSiteGrant struct {
	hostID  string
	view    string
	epoch   uint64
	expires time.Time
}

type nativeSiteSession struct {
	hostID  string
	epoch   uint64
	expires time.Time
}

func (s *Server) NativeSiteListeners() []NativeSite {
	s.nativeSiteMu.Lock()
	defer s.nativeSiteMu.Unlock()
	seen := make(map[string]bool, len(s.nativeSites))
	out := make([]NativeSite, 0, len(s.nativeSites))
	for _, site := range s.nativeSites {
		if !seen[site.Listen] {
			seen[site.Listen] = true
			out = append(out, site)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostID < out[j].HostID })
	return out
}

// NativeSiteHandler serves a Magpie UI on its own origin. The single-use grant
// is exchanged for a server-side portal cookie; the native Magpie key stays
// inside the reverse proxy.
func (s *Server) NativeSiteHandler(hostID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Tracked like every request of the main origin: Close cancels the long-lived
		// streams of the framed Magpie instead of leaving Shutdown to wait for them.
		ctx, requestID, admitted := s.enter(r.Context())
		if !admitted {
			http.Error(w, "server closing", http.StatusServiceUnavailable)
			return
		}
		defer s.leave(requestID)
		s.serveNativeSite(w, r.WithContext(ctx), hostID)
	})
}

func (s *Server) resolveNativeSite(ctx context.Context, hostID string) (NativeSite, bool) {
	for {
		s.nativeSiteMu.Lock()
		if site, ok := s.nativeSites[hostID]; ok {
			s.nativeSiteMu.Unlock()
			return site, true
		}
		if s.nativeSiteResolver == nil || hostID == "local" {
			s.nativeSiteMu.Unlock()
			return NativeSite{}, false
		}
		// One resolution per host, so a concurrent opener never opens a second site.
		if pending, ok := s.nativeSitePending[hostID]; ok {
			s.nativeSiteMu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return NativeSite{}, false
			}
		}
		pending := make(chan struct{})
		s.nativeSitePending[hostID] = pending
		s.nativeSiteMu.Unlock()
		site, ok := s.resolveNativeSiteOnce(ctx, hostID)
		s.nativeSiteMu.Lock()
		delete(s.nativeSitePending, hostID)
		close(pending)
		s.nativeSiteMu.Unlock()
		return site, ok
	}
}

func (s *Server) resolveNativeSiteOnce(ctx context.Context, hostID string) (NativeSite, bool) {
	resolver := s.nativeSiteResolver
	if resolver == nil {
		return NativeSite{}, false
	}
	site, err := resolver(ctx, hostID)
	if err != nil || site.HostID != hostID || validateNativeSite(site) != nil {
		return NativeSite{}, false
	}
	root, rootErr := url.Parse(s.cfg.PublicURL)
	origin, originErr := url.Parse(site.Origin)
	if rootErr != nil || originErr != nil || origin.Scheme != root.Scheme || origin.Host == root.Host {
		return NativeSite{}, false
	}
	s.nativeSiteMu.Lock()
	defer s.nativeSiteMu.Unlock()
	if existing, ok := s.nativeSites[hostID]; ok {
		return existing, true
	}
	// A resolver may introduce its own loopback listener; hosts never share one origin.
	for _, existing := range s.nativeSites {
		if existing.Origin == site.Origin {
			return NativeSite{}, false
		}
	}
	s.nativeSites[hostID] = site
	return site, true
}

func (s *Server) nativeSiteForRequest(hostID string, r *http.Request) (NativeSite, bool) {
	s.nativeSiteMu.Lock()
	defer s.nativeSiteMu.Unlock()
	if hostID != "" {
		site, ok := s.nativeSites[hostID]
		return site, ok
	}
	var found NativeSite
	for _, site := range s.nativeSites {
		origin, err := url.Parse(site.Origin)
		if err == nil && origin.Host == r.Host {
			if found.HostID != "" {
				return NativeSite{}, false
			}
			found = site
		}
	}
	return found, found.HostID != ""
}

func (s *Server) openNativeMagpie(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	hostID, ok := hostIDForOpenPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "magpie_host_not_found")
		return
	}
	site, ok := s.resolveNativeSite(r.Context(), hostID)
	if !ok {
		status, code := http.StatusNotFound, "magpie_host_not_found"
		if hostID == "local" || s.nativeSiteResolver != nil {
			status, code = http.StatusServiceUnavailable, "magpie_not_running"
		}
		writeError(w, status, code)
		return
	}
	if site.Handler == nil {
		writeError(w, http.StatusServiceUnavailable, "magpie_not_running")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	view, err := readNativeView(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "grant_unavailable")
		return
	}
	grant := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	s.nativeSiteMu.Lock()
	s.pruneNativeSitesLocked(time.Now())
	if len(s.nativeGrants)+len(s.nativeSessions) >= 512 {
		s.nativeSiteMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "magpie_site_busy")
		return
	}
	s.nativeGrants[grant] = nativeSiteGrant{hostID: hostID, view: view, epoch: s.nativeSiteEpoch, expires: time.Now().Add(nativeGrantTTL)}
	s.nativeSiteMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{
		"url":    strings.TrimRight(site.Origin, "/") + nativeEnterPath + "#" + grant,
		"origin": site.Origin,
	})
}

func hostIDForOpenPath(path string) (string, bool) {
	const prefix = "/api/hosts/"
	const suffix = "/magpie/open"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return id, nativeViewPattern.MatchString(id)
}

func readNativeView(body io.Reader) (string, error) {
	if body == nil {
		return "agents", nil
	}
	data, err := io.ReadAll(io.LimitReader(body, nativeOpenLimit+1))
	if err != nil || len(data) > nativeOpenLimit {
		return "", errors.New("native Magpie open request is too large")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "agents", nil
	}
	var input struct {
		View string `json:"view"`
	}
	if err := decodeStrictJSON(data, &input); err != nil {
		return "", err
	}
	if input.View == "" {
		return "agents", nil
	}
	if !nativeViewPattern.MatchString(input.View) {
		return "", errors.New("native Magpie view is invalid")
	}
	return input.View, nil
}

func (s *Server) serveNativeSite(w http.ResponseWriter, r *http.Request, hostID string) {
	site, ok := s.nativeSiteForRequest(hostID, r)
	if !ok || site.Handler == nil {
		http.NotFound(w, r)
		return
	}
	hostID = site.HostID
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !validNativeSiteAuthority(r, site) {
		http.Error(w, "request rejected", http.StatusForbidden)
		return
	}
	if r.URL.Query().Has("k") {
		http.Error(w, "native web keys are not accepted in URLs", http.StatusBadRequest)
		return
	}
	if r.URL.Path == nativeEnterPath {
		s.serveNativeEntry(w, r)
		return
	}
	if r.URL.Path == nativeEnterJS {
		s.serveNativeEntryJS(w, r)
		return
	}
	if r.URL.Path == nativeGrantPath {
		s.consumeNativeGrant(w, r, site)
		return
	}
	// the page's own frame carries no cookie: its run token is the key, as the child's guard says
	if own, isPage := nativePluginPagePath(r.URL.Path); isPage {
		// This branch skips the session check, so the path must name exactly the file it
		// appears to: dot segments could climb out of the page folder into Magpie's API.
		if !cleanPluginPath(r.URL.Path) {
			http.Error(w, "request rejected", http.StatusBadRequest)
			return
		}
		site.Handler.ServeHTTP(&nativePluginPageWriter{ResponseWriter: w, own: own, site: nativeSiteAuthority(site.Origin), root: s.cfg.PublicURL}, r)
		return
	}
	cookieName := nativePortalCookie(hostID, s.cookieName)
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == cookieName {
			count++
			value = cookie.Value
		}
	}
	s.nativeSiteMu.Lock()
	session, exists := s.nativeSessions[value]
	valid := count == 1 && exists && session.hostID == hostID && session.epoch == s.nativeSiteEpoch && time.Now().Before(session.expires)
	s.nativeSiteMu.Unlock()
	if !valid {
		http.Error(w, "native Magpie authorization required", http.StatusUnauthorized)
		return
	}
	site.Handler.ServeHTTP(w, r)
}

// nativeSiteAuthority is a site origin's host:port, as a policy's host-source names it.
func nativeSiteAuthority(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return u.Host
}

// nativePluginPagePrefix is where a community plugin's own page's files are served.
const nativePluginPagePrefix = "/_magpie/plugin/"

// nativePluginPagePath is the folder of the run's page token this request names, when it does.
func nativePluginPagePath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, nativePluginPagePrefix)
	if !ok {
		return "", false
	}
	token, _, ok := strings.Cut(rest, "/")
	if !ok || len(token) != 32 {
		return "", false
	}
	for _, char := range token {
		if !(char >= 'a' && char <= 'f' || char >= '0' && char <= '9') {
			return "", false
		}
	}
	return nativePluginPagePrefix + token + "/", true
}

// cleanPluginPath accepts a path only in its canonical form (one trailing slash allowed).
func cleanPluginPath(p string) bool {
	trimmed := strings.TrimSuffix(p, "/")
	return !strings.Contains(p, "//") && path.Clean(trimmed) == trimmed
}

// nativePluginPageWriter rewrites one header of a plugin page's own files, on the
// way out; Unwrap keeps the app's own writer reachable by http.ResponseController.
type nativePluginPageWriter struct {
	http.ResponseWriter
	own  string
	site string
	root string
	done bool
}

func (w *nativePluginPageWriter) WriteHeader(status int) {
	w.rewrite()
	w.ResponseWriter.WriteHeader(status)
}

func (w *nativePluginPageWriter) Write(body []byte) (int, error) {
	w.rewrite()
	return w.ResponseWriter.Write(body)
}

func (w *nativePluginPageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *nativePluginPageWriter) rewrite() {
	if w.done {
		return
	}
	w.done = true
	if policy := w.Header().Get("Content-Security-Policy"); policy != "" {
		w.Header().Set("Content-Security-Policy", nativePluginPagePolicy(policy, w.own, w.site, w.root))
	}
}

// nativePluginPagePolicy re-anchors the page's own folder to the public origin of
// that host and adds this installation to the frames allowed; the rest is the child's.
func nativePluginPagePolicy(policy, own, site, root string) string {
	parts := strings.Split(policy, ";")
	for i, part := range parts {
		directive := strings.TrimSpace(part)
		if directive == "" {
			continue
		}
		lead := part[:len(part)-len(strings.TrimLeft(part, " \t"))]
		name, value, _ := strings.Cut(directive, " ")
		value = strings.TrimSpace(value)
		if name == "frame-ancestors" {
			if value == "'self'" && root != "" {
				parts[i] = lead + name + " 'self' " + root
			}
			continue
		}
		tokens := strings.Fields(value)
		changed := false
		for j, token := range tokens {
			if at := strings.Index(token, own); at > 0 {
				tokens[j] = site + token[at:]
				changed = true
			}
		}
		if changed {
			parts[i] = lead + name + " " + strings.Join(tokens, " ")
		}
	}
	return strings.Join(parts, ";")
}

func (s *Server) serveNativeEntry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.Error(w, "native Magpie grant rejected", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; base-uri 'none'; object-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Opening Magpie</title><script src="/_workbench/magpie/enter.js"></script><p>Opening Magpie…</p>`)
}

func (s *Server) serveNativeEntryJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		http.Error(w, "native Magpie grant rejected", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, `(async()=>{const grant=location.hash.slice(1);history.replaceState(null,"",location.pathname);if(!/^[A-Za-z0-9_-]{43}$/.test(grant)){document.body.textContent="Magpie authorization failed";return}try{const r=await fetch("/_workbench/magpie/consume",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({grant})});if(!r.ok)throw Error();const v=await r.json();location.replace(v.url)}catch{document.body.textContent="Magpie authorization failed"}})();`)
}

func (s *Server) consumeNativeGrant(w http.ResponseWriter, r *http.Request, site NativeSite) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		http.Error(w, "native Magpie grant rejected", http.StatusForbidden)
		return
	}
	body := http.MaxBytesReader(w, r.Body, nativeOpenLimit)
	var input struct {
		Grant string `json:"grant"`
	}
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || len(input.Grant) != 43 {
		http.Error(w, "native Magpie grant rejected", http.StatusForbidden)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		http.Error(w, "native Magpie grant rejected", http.StatusForbidden)
		return
	}
	s.nativeSiteMu.Lock()
	grant, exists := s.nativeGrants[input.Grant]
	delete(s.nativeGrants, input.Grant)
	valid := exists && grant.hostID == site.HostID && grant.epoch == s.nativeSiteEpoch && time.Now().Before(grant.expires)
	if !valid {
		s.nativeSiteMu.Unlock()
		http.Error(w, "native Magpie grant rejected", http.StatusForbidden)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.nativeSiteMu.Unlock()
		http.Error(w, "native Magpie authorization unavailable", http.StatusInternalServerError)
		return
	}
	sessionToken := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	s.nativeSessions[sessionToken] = nativeSiteSession{hostID: site.HostID, epoch: s.nativeSiteEpoch, expires: time.Now().Add(nativePortalTTL)}
	s.nativeSiteMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: nativePortalCookie(site.HostID, s.cookieName), Value: sessionToken, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(site.Origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: int(nativePortalTTL.Seconds())})
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, map[string]string{"url": "/?view=" + url.QueryEscape(grant.view)})
}

func validNativeSiteAuthority(r *http.Request, site NativeSite) bool {
	u, err := url.Parse(site.Origin)
	if err != nil || u.Host == "" || r.Host != u.Host || len(r.Header.Values("Host")) != 0 {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || len(origins) == 1 && origins[0] != site.Origin {
		return false
	}
	if isUpgradeRequest(r) {
		return r.Method == http.MethodGet && len(origins) == 1 && origins[0] == site.Origin
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return len(origins) == 1 && origins[0] == site.Origin
	}
	return true
}

func nativePortalCookie(hostID, instance string) string {
	digest := sha256.Sum256([]byte(instance + "\x00" + hostID))
	return nativeSiteCookie + hex.EncodeToString(digest[:8])
}

func (s *Server) pruneNativeSitesLocked(now time.Time) {
	for token, grant := range s.nativeGrants {
		if grant.epoch != s.nativeSiteEpoch || !now.Before(grant.expires) {
			delete(s.nativeGrants, token)
		}
	}
	for token, session := range s.nativeSessions {
		if session.epoch != s.nativeSiteEpoch || !now.Before(session.expires) {
			delete(s.nativeSessions, token)
		}
	}
}

func (s *Server) revokeNativeSites(w http.ResponseWriter) {
	s.revokeNativeSiteState()
	s.nativeSiteMu.Lock()
	sites := make(map[string]NativeSite, len(s.nativeSites))
	for hostID, site := range s.nativeSites {
		sites[hostID] = site
	}
	s.nativeSiteMu.Unlock()
	for hostID, site := range sites {
		http.SetCookie(w, &http.Cookie{Name: nativePortalCookie(hostID, s.cookieName), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(site.Origin, "https://"), SameSite: http.SameSiteLaxMode})
	}
}

func (s *Server) revokeNativeSiteState() {
	s.nativeSiteMu.Lock()
	s.nativeSiteEpoch++
	s.nativeGrants = make(map[string]nativeSiteGrant)
	s.nativeSessions = make(map[string]nativeSiteSession)
	s.nativeSiteMu.Unlock()
}

func validateNativeSite(site NativeSite) error {
	if site.HostID == "" || site.Handler == nil {
		return errors.New("native Magpie site requires a host and handler")
	}
	host, port, err := net.SplitHostPort(site.Listen)
	if err != nil || port == "" || host == "" {
		return errors.New("native Magpie site listener must be a numeric loopback host:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("native Magpie site listener must be loopback-only")
	}
	listenPort, err := strconv.Atoi(port)
	if err != nil || listenPort < 1 || listenPort > 65535 {
		return errors.New("native Magpie site listener port is invalid")
	}
	u, err := url.Parse(site.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("native Magpie site origin is invalid")
	}
	if normalized, err := config.NormalizePublicURL(site.Origin); err != nil || normalized != site.Origin {
		if localOrigin := canonicalLocalhostSiteOrigin(site.Origin); localOrigin == "" || localOrigin != site.Origin {
			return errors.New("native Magpie site origin must be canonical")
		}
	}
	return nil
}

func canonicalLocalhostSiteOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || !strings.HasSuffix(host, ".localhost") {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return ""
			}
		}
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 || port == "80" {
			return ""
		}
		return "http://" + net.JoinHostPort(host, port)
	}
	if strings.HasSuffix(u.Host, ":") {
		return ""
	}
	return "http://" + host
}
