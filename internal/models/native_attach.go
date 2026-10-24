package models

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Attach to the user's existing Magpie without starting another gateway or
// taking ownership of its lifecycle or agent configuration.
func attachNativeProcess(path, publicURL string, sitePort int) (*nativeProcess, error) {
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return nil, errors.New("Magpie attachment must be an absolute private regular file (at most 4096 bytes)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read Magpie attachment")
	}
	var config struct {
		WebURL     string `json:"web_url"`
		GatewayURL string `json:"gateway_url"`
		WebKey     string `json:"web_key"`
	}
	if json.Unmarshal(data, &config) != nil {
		return nil, errors.New("invalid Magpie attachment JSON")
	}
	web, webPort, err := attachedLoopbackURL(config.WebURL)
	if err != nil {
		return nil, err
	}
	_, gatewayPort, err := attachedLoopbackURL(config.GatewayURL)
	if err != nil {
		return nil, err
	}
	if len(config.WebKey) < 16 || len(config.WebKey) > 256 || strings.Trim(config.WebKey, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~") != "" {
		return nil, errors.New("invalid Magpie attachment web key")
	}
	origin, err := siteOrigin(publicURL, sitePort)
	if err != nil {
		return nil, err
	}
	p := &nativeProcess{
		webURL: config.WebURL, gateway: config.GatewayURL, webHost: web.Host,
		webKey: config.WebKey, siteOrigin: origin,
		ports:     nativePorts{Site: sitePort, Web: webPort, Gateway: gatewayPort},
		client:    newNativeHTTPClient(),
		startedAt: time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), nativeProbeTimeout)
	defer cancel()
	// The sign-in page also answers 200: verify the authenticated API as well.
	if _, err := p.NativeJSON(ctx, "/api/state"); err != nil {
		return nil, errors.New("existing Magpie authentication or connection failed")
	}
	p.readyAt = time.Now().UTC()
	p.record("attach", "connected to existing native Magpie; lifecycle stays external")
	return p, nil
}

func attachedLoopbackURL(value string) (*url.URL, int, error) {
	u, err := url.Parse(value)
	if err == nil && u.Scheme == "http" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" {
		address, ipErr := netip.ParseAddr(u.Hostname())
		port, portErr := strconv.Atoi(u.Port())
		if ipErr == nil && address.IsLoopback() && address.Zone() == "" && portErr == nil && port > 0 && port <= 65535 {
			return u, port, nil
		}
	}
	return nil, 0, errors.New("Magpie attachment URLs must be HTTP loopback origins with an explicit port")
}
