package hosts

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const maxHostAgentAuthResponse = 4096

func (s *Server) hostAgentProxyRoute(surface string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.SSH == nil {
			fail(w, http.StatusServiceUnavailable, errors.New("servicio SSH no disponible"))
			return
		}
		targetPath, inference, err := hostAgentProxyPath(r, surface)
		if err != nil {
			fail(w, http.StatusBadRequest, errors.New("ruta de host-agent inválida"))
			return
		}
		callerAuth := ""
		if inference {
			callerAuth, err = hostAgentCallerAuthorization(r)
			if err != nil {
				fail(w, http.StatusUnauthorized, errHostAgentCallerAuth)
				return
			}
		}
		err = s.SSH.SSHOperation(r.Context(), r.PathValue("id"), func(opCtx context.Context, client *ssh.Client, identity Identity) error {
			state, err := s.inspectForProxy(opCtx, client, identity)
			if err != nil {
				return err
			}
			defer clear(state.token)
			if !state.status.Ready || !validHostAgentToken(state.token) {
				return errHostAgentNotReady
			}
			if inference && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(callerAuth, "Bearer ")), state.token) == 1 {
				return errHostAgentCallerAuth
			}
			invalidate := func() { s.agentCache.invalidate(identity.HostID) }
			if err := serveHostAgentProxy(opCtx, w, r, client, state.ready, state.token, targetPath, inference, callerAuth, invalidate); err != nil {
				invalidate()
				return err
			}
			return nil
		})
		if err != nil {
			writeHostAgentError(w, err)
		}
	}
}

// inspectForProxy returns a recent verified inspection when one exists for this
// exact SSH identity, and otherwise performs the full inspection (SFTP walk and
// remote binary digest) and remembers a ready result briefly. A stale hit is
// self-correcting: a failed tunnel or helper login invalidates the entry.
func (s *Server) inspectForProxy(ctx context.Context, client *ssh.Client, identity Identity) (remoteHostAgent, error) {
	if state, ok := s.agentCache.get(identity); ok {
		return state, nil
	}
	state, err := inspectRemoteHostAgent(ctx, client, identity)
	if err != nil {
		s.agentCache.invalidate(identity.HostID)
		return state, err
	}
	s.agentCache.put(identity, state)
	return state, nil
}

func hostAgentProxyPath(r *http.Request, surface string) (string, bool, error) {
	id := r.PathValue("id")
	if id == "" || id == "local" || strings.ContainsAny(id, "/\\\r\n\x00") {
		return "", false, errors.New("invalid host id")
	}
	base := "/api/hosts/" + id + "/" + surface
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		return "", false, errors.New("invalid host route")
	}
	suffix := strings.TrimPrefix(r.URL.Path, base)
	for _, segment := range strings.Split(suffix, "/") {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "\\\r\n\x00") {
			return "", false, errors.New("invalid helper path")
		}
	}
	prefixes := map[string]string{
		"models": "/api/models",
	}
	prefix, ok := prefixes[surface]
	if !ok {
		return "", false, errors.New("invalid helper surface")
	}
	if surface == "models" && hostAgentInferencePath(suffix) {
		return suffix, true, nil
	}
	return prefix + suffix, false, nil
}

func hostAgentInferencePath(path string) bool {
	return path == "/v1" || strings.HasPrefix(path, "/v1/") ||
		path == "/v1beta" || strings.HasPrefix(path, "/v1beta/") ||
		path == "/backend-api/codex" || strings.HasPrefix(path, "/backend-api/codex/")
}

func hostAgentCallerAuthorization(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", errHostAgentCallerAuth
	}
	fields := strings.Fields(values[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) < 8 || len(fields[1]) > 4096 {
		return "", errHostAgentCallerAuth
	}
	for _, char := range fields[1] {
		if char <= 0x20 || char >= 0x7f {
			return "", errHostAgentCallerAuth
		}
	}
	for _, cookie := range r.Cookies() {
		if subtle.ConstantTimeCompare([]byte(fields[1]), []byte(cookie.Value)) == 1 {
			return "", errHostAgentCallerAuth
		}
	}
	return "Bearer " + fields[1], nil
}

func serveHostAgentProxy(ctx context.Context, w http.ResponseWriter, r *http.Request, client *ssh.Client, ready HostAgentReady, token []byte, helperPath string, inference bool, callerAuth string, invalidate func()) error {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ready.Port))
	helperOrigin := "http://" + address
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialCtx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || target != address {
				return nil, errors.New("host-agent tunnel target rejected")
			}
			if err := dialCtx.Err(); err != nil {
				return nil, err
			}
			conn, err := client.Dial("tcp", address)
			if err != nil {
				return nil, err
			}
			context.AfterFunc(dialCtx, func() { _ = conn.Close() })
			return conn, nil
		},
	}
	defer transport.CloseIdleConnections()

	helperCookie, err := authenticateHostAgent(ctx, transport, helperOrigin, address, token)
	if err != nil {
		return err
	}
	targetURL := &url.URL{Scheme: "http", Host: address}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			out := proxyRequest.Out
			out.URL = &url.URL{Scheme: targetURL.Scheme, Host: targetURL.Host, Path: helperPath, RawQuery: r.URL.RawQuery}
			out.Host = address
			for name := range out.Header {
				lower := strings.ToLower(name)
				if lower == "host" || lower == "origin" || lower == "cookie" || lower == "authorization" || lower == "proxy-authorization" || lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || lower == "x-real-ip" || lower == "cf-connecting-ip" || lower == "true-client-ip" {
					out.Header.Del(name)
				}
			}
			out.Header.Set("Cookie", helperCookie)
			out.Header.Set("Origin", helperOrigin)
			if inference {
				out.Header.Set("Authorization", callerAuth)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if len(response.Header.Values("Location")) != 0 {
				return errors.New("host-agent redirect blocked")
			}
			for _, name := range []string{"Set-Cookie", "Content-Location", "Proxy-Authenticate", "Refresh", "Alt-Svc", "Link"} {
				response.Header.Del(name)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			invalidate()
			fail(w, http.StatusBadGateway, errors.New("host-agent no disponible a través del túnel SSH"))
		},
		FlushInterval: -1,
	}
	proxy.ServeHTTP(w, r)
	return nil
}

func authenticateHostAgent(ctx context.Context, transport *http.Transport, origin, host string, token []byte) (string, error) {
	body, err := json.Marshal(struct {
		Token string `json:"token"`
	}{Token: string(token)})
	if err != nil {
		return "", errors.New("no se pudo autenticar el host-agent")
	}
	authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(authCtx, http.MethodPost, origin+"/api/auth", strings.NewReader(string(body)))
	if err != nil {
		return "", errors.New("no se pudo autenticar el host-agent")
	}
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("la autenticación interna del host-agent falló")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxHostAgentAuthResponse+1))
	if err != nil || len(data) > maxHostAgentAuthResponse || response.StatusCode != http.StatusOK {
		return "", errors.New("la autenticación interna del host-agent falló")
	}
	var result struct {
		OK bool `json:"ok"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || !result.OK {
		return "", errors.New("la autenticación interna del host-agent falló")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return "", errors.New("la autenticación interna del host-agent falló")
	}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		return "", errors.New("la cookie interna del host-agent no es válida")
	}
	cookie := cookies[0]
	if !strings.HasPrefix(cookie.Name, "workbench_session_") || cookie.Value != string(token) || cookie.Path != "/" || cookie.Domain != "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		return "", errors.New("la cookie interna del host-agent no es válida")
	}
	return cookie.Name + "=" + cookie.Value, nil
}
