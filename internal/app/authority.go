package app

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

func (s *Server) authorizedAuthority(r *http.Request) bool {
	if len(r.Header.Values("Host")) != 0 || r.Host == "" || strings.ContainsAny(r.Host, "\r\n\t /\\") {
		return false
	}
	origins := r.Header.Values("Origin")
	if isUpgradeRequest(r) && (r.Method != http.MethodGet || len(origins) != 1 || origins[0] != s.cfg.PublicURL) {
		return false
	}
	if r.URL.Path == "/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) && loopbackPeer(r.RemoteAddr) && s.localHealthHost(r.Host) {
		return len(origins) == 0 || len(origins) == 1 && origins[0] == s.cfg.PublicURL
	}
	public, err := url.Parse(s.cfg.PublicURL)
	if err != nil || !strings.EqualFold(r.Host, public.Host) {
		return false
	}
	if len(origins) > 1 || len(origins) == 1 && origins[0] != s.cfg.PublicURL {
		return false
	}
	secure := public.Scheme == "https"
	if secure && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && len(origins) != 1 && !validInferenceRequest(r, s.cfg.AuthToken) {
		return false
	}

	forwardedProto := r.Header.Values("X-Forwarded-Proto")
	trustedProxy := s.trustedProxy(r.RemoteAddr)
	if len(forwardedProto) > 0 {
		if !trustedProxy || len(forwardedProto) != 1 || forwardedProto[0] != public.Scheme {
			return false
		}
	}
	if secure {
		if r.TLS == nil && (!trustedProxy || len(forwardedProto) != 1 || forwardedProto[0] != "https") {
			return false
		}
		if r.TLS != nil && len(forwardedProto) != 0 && !trustedProxy {
			return false
		}
	} else if r.TLS != nil {
		return false
	}
	return true
}

// isUpgradeRequest makes Origin mandatory even for GET-based WebSocket
// handshakes. Ordinary GETs keep their existing same-origin behavior.
func isUpgradeRequest(r *http.Request) bool {
	for _, value := range r.Header.Values("Upgrade") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func (s *Server) localHealthHost(host string) bool {
	listenHost, port, err := net.SplitHostPort(s.cfg.Listen)
	if err != nil {
		return false
	}
	allowed := []string{
		net.JoinHostPort(listenHost, port),
		net.JoinHostPort("127.0.0.1", port),
		net.JoinHostPort("::1", port),
		net.JoinHostPort("localhost", port),
	}
	for _, candidate := range allowed {
		if strings.EqualFold(host, candidate) {
			return true
		}
	}
	return false
}

func (s *Server) trustedProxy(remote string) bool {
	address := remoteAddress(remote)
	if !address.IsValid() {
		return false
	}
	for _, prefix := range s.cfg.TrustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func remoteAddress(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return address.WithZone("")
}

func loopbackPeer(remote string) bool {
	address := remoteAddress(remote)
	return address.IsValid() && address.IsLoopback()
}
