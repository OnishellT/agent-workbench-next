package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadResolvesOptionalSIMOrigin(t *testing.T) {
	t.Setenv("WORKBENCH_SIM_URL", "")
	args := []string{"--state-dir", filepath.Join(t.TempDir(), "state"), "--listen", "127.0.0.1:18888"}
	cfg, err := Load(args)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SIMURL != "" {
		t.Fatalf("unconfigured SIM origin = %q, want empty", cfg.SIMURL)
	}

	t.Setenv("WORKBENCH_SIM_URL", "http://127.0.0.1:33500")
	fromEnv, err := Load(args)
	if err != nil || fromEnv.SIMURL != "http://127.0.0.1:33500" {
		t.Fatalf("environment SIM origin = %q, %v", fromEnv.SIMURL, err)
	}

	flagged, err := Load(append(append([]string{}, args...), "--sim-url", "HTTPS://sim.example.com:443/"))
	if err != nil || flagged.SIMURL != "https://sim.example.com" {
		t.Fatalf("flag SIM origin = %q, %v; want the flag to override the environment", flagged.SIMURL, err)
	}

	secure, err := Load(append(append([]string{}, args...), "--public-url", "https://workbench.example", "--sim-url", "https://sim.workbench.example"))
	if err != nil || secure.SIMURL != "https://sim.workbench.example" {
		t.Fatalf("separate same-site HTTPS origin = %q, %v", secure.SIMURL, err)
	}
}

func TestLoadRejectsUnusableSIMOrigin(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "public HTTP", args: []string{"--sim-url", "http://sim.example.com"}},
		{name: "credentials", args: []string{"--sim-url", "https://user:pass@sim.example.com"}},
		{name: "path", args: []string{"--sim-url", "https://sim.example.com/editor"}},
		{name: "query", args: []string{"--sim-url", "https://sim.example.com/?q=1"}},
		{name: "fragment", args: []string{"--sim-url", "https://sim.example.com#editor"}},
		{name: "not an origin", args: []string{"--sim-url", "127.0.0.1:33500"}},
		{name: "same origin as Workbench", args: []string{"--public-url", "https://workbench.example", "--sim-url", "https://workbench.example/"}},
		{name: "same loopback origin as Workbench", args: []string{"--sim-url", "http://127.0.0.1:18888"}},
		{name: "HTTPS Workbench cannot embed HTTP loopback", args: []string{"--public-url", "https://workbench.example", "--sim-url", "http://127.0.0.1:33500"}},
		{name: "HTTPS cookies are not isolated by port", args: []string{"--public-url", "https://workbench.example", "--sim-url", "https://workbench.example:8443"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--state-dir", filepath.Join(t.TempDir(), "state")}, test.args...)
			if cfg, err := Load(args); err == nil {
				t.Fatalf("Load accepted SIM origin %v as %q", test.args, cfg.SIMURL)
			}
		})
	}
}

func TestValidateRejectsSIMOriginEqualToWorkbench(t *testing.T) {
	base := Config{
		StateDir:  filepath.Join(t.TempDir(), "state"),
		Listen:    "127.0.0.1:18888",
		PublicURL: "http://127.0.0.1:18888",
		AuthToken: strings.Repeat("A", 32),
	}
	accepted := base
	accepted.SIMURL = "http://127.0.0.1:33500"
	if err := accepted.Validate(); err != nil {
		t.Fatalf("dedicated SIM origin rejected: %v", err)
	}
	for _, sim := range []string{base.PublicURL, "https://sim.example.com/path", "sim.example.com"} {
		rejected := base
		rejected.SIMURL = sim
		if err := rejected.Validate(); err == nil {
			t.Errorf("Validate accepted sim-url %q", sim)
		}
	}
}
