package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"workbench/internal/app"
)

// remoteNativeSite resolves one host's native Magpie site: the panel authorizes itself
// with a first-party cookie, so its origin must stay in the installation's own site.
// A public hostname gets the label as a subdomain; a loopback http installation (no
// subdomains) gets its own port per host of the address the shell already uses.
// open binds the listener and answers with the address it really got.
func remoteNativeSite(publicURL, baseOrigin, siteListen, hostID string, open func(string) (string, error)) (app.NativeSite, error) {
	root, rootErr := url.Parse(publicURL)
	if baseOrigin == "" && rootErr == nil && root.Scheme == "http" && loopbackAddress(root.Hostname()) {
		baseOrigin = publicURL // remote-only Docker has no local Magpie origin
	}
	u, err := url.Parse(baseOrigin)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(hostID) == "" {
		return app.NativeSite{}, errors.New("remote native Magpie origin is invalid")
	}
	if rootErr == nil && root.Scheme == "http" && loopbackAddress(root.Hostname()) {
		address, err := open(net.JoinHostPort(root.Hostname(), "0"))
		if err != nil {
			return app.NativeSite{}, fmt.Errorf("native Magpie site for the host: %w", err)
		}
		_, port, err := net.SplitHostPort(address)
		if err != nil || port == "" {
			return app.NativeSite{}, fmt.Errorf("native Magpie site address %q is invalid", address)
		}
		return app.NativeSite{HostID: hostID, Listen: address, Origin: "http://" + net.JoinHostPort(root.Hostname(), port)}, nil
	}
	digest := sha256.Sum256([]byte(hostID))
	label := "h-" + hex.EncodeToString(digest[:16])
	host := u.Hostname()
	if net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		host = label + ".magpie.localhost"
	} else {
		host = label + "." + host
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else {
		u.Host = host
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "", "", "", ""
	return app.NativeSite{HostID: hostID, Listen: siteListen, Origin: u.String()}, nil
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: addr, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
}

func loopbackAddress(host string) bool {
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// siteSet owns the HTTP servers of the native Magpie sites. It grows while the process
// runs: serving a host opens its listener before the URL that names it reaches the browser.
type siteSet struct {
	mu      sync.Mutex
	servers []*http.Server
	// handler is resolved per server because the application that provides it is built
	// after the set is.
	handler func() http.Handler
}

func (s *siteSet) add(server *http.Server) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers = append(s.servers, server)
}

func (s *siteSet) all() []*http.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Server{}, s.servers...)
}

// serve binds listen and serves the native site handler on it in the background; it
// answers with the address it really got (listen may ask for port 0).
func (s *siteSet) serve(listen string) (string, error) {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return "", err
	}
	server := newHTTPServer(listener.Addr().String(), s.handler())
	s.add(server)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("native Magpie site %s: %v", listener.Addr(), err)
		}
	}()
	return listener.Addr().String(), nil
}
