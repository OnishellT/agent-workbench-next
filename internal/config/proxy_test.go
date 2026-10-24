package config

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsNonLoopbackTrustedProxy(t *testing.T) {
	_, err := Load([]string{"--state-dir", filepath.Join(t.TempDir(), "state"), "--trusted-proxy", "192.0.2.1"})
	if err == nil {
		t.Fatal("non-loopback proxy was accepted for the loopback-only listener")
	}
}

func TestTrustedProxyRequiresEntirePrefixToBeLoopback(t *testing.T) {
	cases := []struct {
		input string
		valid bool
	}{
		{input: "127.0.0.0/8", valid: true},
		{input: "127.0.0.1/8", valid: true},
		{input: "::1", valid: true},
		{input: "127.0.0.0/7"},
		{input: "126.0.0.0/7"},
		{input: "::/127"},
		{input: "::ffff:127.0.0.1"},
		{input: "::ffff:127.0.0.0/120"},
	}
	for _, test := range cases {
		t.Run(test.input, func(t *testing.T) {
			prefix, err := parseTrustedProxy(test.input)
			if test.valid {
				if err != nil {
					t.Fatalf("parseTrustedProxy(%q): %v", test.input, err)
				}
			} else if err == nil {
				t.Fatalf("parseTrustedProxy(%q) accepted %s", test.input, prefix)
			}
		})
	}
}

func TestValidateRejectsIPv4MappedTrustedProxy(t *testing.T) {
	config := Config{
		StateDir:  filepath.Join(t.TempDir(), "state"),
		Listen:    defaultListen,
		PublicURL: "http://" + defaultListen,
		AuthToken: strings.Repeat("A", 32),
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("::ffff:127.0.0.1/128"),
		},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("IPv4-mapped trusted proxy was accepted")
	}
}
