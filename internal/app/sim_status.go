package app

import (
	"context"
	"net/http"
	"time"
)

// simStatus tells the SIM tab whether the SIM this installation frames answers. The
// browser cannot probe it itself (connect-src 'self'), and a framed dead address only
// shows the browser's own error page, so the screen asks here before drawing the frame.
func (s *Server) simStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{
		"configured": s.cfg.SIMURL != "",
		"reachable":  s.cfg.SIMURL != "" && probeHTTP(r.Context(), s.cfg.SIMURL),
	})
}

// probeHTTP reports whether something answers HTTP at target: any status counts, since
// the point is that a server is there, not that its page is healthy.
func probeHTTP(ctx context.Context, target string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return false
	}
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return true
}
