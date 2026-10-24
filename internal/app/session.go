package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

const maxAuthBody = 4096

// authFailure tracks consecutive wrong tokens from one client address.
type authFailure struct {
	count int
	until time.Time // no attempt is evaluated before this instant
	last  time.Time
}

const (
	authBackoffBase  = 250 * time.Millisecond
	authBackoffMax   = 30 * time.Second
	authFailureReset = 10 * time.Minute
	authFailureTable = 256
)

// authBlocked reports how long the client must wait before trying the token again.
func (s *Server) authBlocked(client netip.Addr) time.Duration {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if wait := time.Until(s.authFails[client].until); wait > 0 {
		return wait
	}
	return 0
}

func (s *Server) authFailed(client netip.Addr) {
	now := time.Now()
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authFails == nil {
		s.authFails = make(map[netip.Addr]authFailure)
	}
	if len(s.authFails) >= authFailureTable {
		for addr, f := range s.authFails {
			if now.Sub(f.last) > authFailureReset {
				delete(s.authFails, addr)
			}
		}
		if len(s.authFails) >= authFailureTable {
			clear(s.authFails)
		}
	}
	f := s.authFails[client]
	if now.Sub(f.last) > authFailureReset {
		f = authFailure{}
	}
	f.count++
	delay := authBackoffMax
	if f.count < 8 {
		delay = authBackoffBase << (f.count - 1)
	}
	f.until, f.last = now.Add(min(delay, authBackoffMax)), now
	s.authFails[client] = f
}

func (s *Server) authSucceeded(client netip.Addr) {
	s.authMu.Lock()
	delete(s.authFails, client)
	s.authMu.Unlock()
}

func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	mediaTypes := r.Header.Values("Content-Type")
	if len(mediaTypes) != 1 {
		writeError(w, http.StatusUnsupportedMediaType, "application_json_required")
		return
	}
	mediaType, _, err := mime.ParseMediaType(mediaTypes[0])
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "application_json_required")
		return
	}
	client := remoteAddress(r.RemoteAddr)
	if wait := s.authBlocked(client); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	body, err := readAuthBody(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(s.cfg.AuthToken)) != 1 {
		s.authFailed(client)
		writeError(w, http.StatusUnauthorized, "invalid_access_token")
		return
	}
	s.authSucceeded(client)
	session, err := s.newSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_unavailable")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookie(),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type authRequest struct {
	Token string `json:"token"`
}

func readAuthBody(body io.Reader) (authRequest, error) {
	var result authRequest
	if body == nil {
		return result, errors.New("empty body")
	}
	data, err := io.ReadAll(io.LimitReader(body, maxAuthBody+1))
	if err != nil || len(data) > maxAuthBody {
		return result, errors.New("invalid body")
	}
	if err := decodeStrictJSON(data, &result); err != nil {
		return authRequest{}, err
	}
	return result, nil
}

func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) bool {
	cookies := r.Cookies()
	var value string
	count := 0
	for _, cookie := range cookies {
		if cookie.Name == s.cookieName {
			value = cookie.Value
			count++
		}
	}
	if count != 1 || !s.validSession(value) {
		writeError(w, http.StatusUnauthorized, "authentication_required")
		return false
	}
	return true
}

// sessionTTL bounds a dashboard login; the user re-enters the token afterwards.
const sessionTTL = 30 * 24 * time.Hour

// maxSessions caps the map for a single operator with a few browsers; the
// oldest-expiring entries are dropped first instead of refusing the login.
const maxSessions = 32

func (s *Server) newSession() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	for token, expires := range s.sessions {
		if !expires.After(now) {
			delete(s.sessions, token)
		}
	}
	for len(s.sessions) >= maxSessions {
		oldest, oldestExpiry := "", time.Time{}
		for token, expires := range s.sessions {
			if oldest == "" || expires.Before(oldestExpiry) {
				oldest, oldestExpiry = token, expires
			}
		}
		delete(s.sessions, oldest)
	}
	s.sessions[value] = now.Add(sessionTTL)
	return value, nil
}

func (s *Server) validSession(value string) bool {
	if value == "" {
		return false
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	expires, ok := s.sessions[value]
	if !ok {
		return false
	}
	if !expires.After(time.Now()) {
		delete(s.sessions, value)
		return false
	}
	return true
}

func (s *Server) dropSession(r *http.Request) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	for _, cookie := range r.Cookies() {
		if cookie.Name == s.cookieName {
			delete(s.sessions, cookie.Value)
		}
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	s.dropSession(r)
	s.revokeNativeSites(w)
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookie(),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) secureCookie() bool {
	public, _ := url.Parse(s.cfg.PublicURL)
	return public != nil && public.Scheme == "https"
}

func instanceCookieName(token string) string {
	digest := sha256.Sum256([]byte(token))
	return "workbench_session_" + hex.EncodeToString(digest[:])[:16]
}
