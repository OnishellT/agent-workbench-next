package foundryloop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"strings"
	"syscall"
)

// Handler serves Foundry under Prefix. The caller (internal/app) has already checked the
// Workbench session, Host and Origin; this handler replaces the browser's credentials with
// Foundry's own token and drops everything else that identifies the browser, so Foundry
// only ever sees a loopback client that owns its store.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == Prefix {
			target := Prefix + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
			return
		}
		inner := strings.TrimPrefix(r.URL.Path, Prefix)
		if !strings.HasPrefix(inner, "/") || hasDotSegment(inner) {
			writeError(w, http.StatusBadRequest, "bad_path", "")
			return
		}
		address, token, err := s.target(r.Context())
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, context.Canceled) {
				status = http.StatusRequestTimeout
			}
			w.Header().Set("Retry-After", "5")
			writeError(w, status, "foundry_unavailable", err.Error())
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.URL.Scheme = "http"
				pr.Out.URL.Host = address
				pr.Out.URL.Path, pr.Out.URL.RawPath = inner, ""
				pr.Out.Host = address
				header := pr.Out.Header
				for _, name := range []string{"Origin", "Referer", "Cookie", "Authorization", "X-Foundry-Token"} {
					header.Del(name)
				}
				header.Set("X-Foundry-Token", token)
			},
			ModifyResponse: func(response *http.Response) error {
				// Foundry forbids framing; Workbench embeds it from its own origin only.
				if csp := response.Header.Get("Content-Security-Policy"); csp != "" {
					response.Header.Set("Content-Security-Policy", strings.Replace(csp, "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
				}
				response.Header.Del("Set-Cookie")
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				if errors.Is(err, syscall.ECONNREFUSED) {
					s.markDown()
				}
				if errors.Is(err, context.Canceled) {
					return
				}
				writeError(w, http.StatusBadGateway, "foundry_unreachable", "Foundry did not answer; it restarts on the next request")
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

func hasDotSegment(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." || segment == "." || strings.ContainsAny(segment, "\\\x00") {
			return true
		}
	}
	return false
}

func writeError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	body := map[string]string{"error": code}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body)
}
