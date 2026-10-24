package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"
)

func TestVersionDoesNotStartApplication(t *testing.T) {
	if err := run([]string{"--version"}); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFlagFailsBeforeStartingApplication(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := run([]string{"--unknown-migration-flag"}); err == nil {
		t.Fatal("unknown flag was accepted")
	}
}

func TestHostAgentDispatchesFlagsWithoutStartingRoot(t *testing.T) {
	if err := run([]string{"host-agent", "--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("host-agent help: expected flag.ErrHelp, observed %v", err)
	}
}

func TestRemoteNativeSiteStaysInTheInstallationsOwnSite(t *testing.T) {
	const public = "https://workbench.example.com"
	const site = "https://magpie.workbench.example.com"
	first, err := remoteNativeSite(public, site, "127.0.0.1:39004", "host_one", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := remoteNativeSite(public, site, "127.0.0.1:39004", "host-two", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Origin == second.Origin || first.Origin == site || !strings.HasSuffix(first.Origin, ".magpie.workbench.example.com") || strings.Contains(first.Origin, "host_one") {
		t.Fatalf("remote origins are not unique same-site hostnames: %q %q", first.Origin, second.Origin)
	}
	if first.Listen != "127.0.0.1:39004" || first.HostID != "host_one" {
		t.Fatalf("hostname site does not use the installation listener: %+v", first)
	}
	// A loopback http installation has no subdomains: every host gets its own port of the
	// address the shell already uses, or the browser would refuse the panel's own cookie
	// inside the frame.
	bound := map[string]string{}
	open := func(listen string) (string, error) {
		if listen != "127.0.0.1:0" {
			t.Errorf("loopback site asked to bind %q", listen)
		}
		address := fmt.Sprintf("127.0.0.1:%d", 46610+len(bound))
		bound[address] = listen
		return address, nil
	}
	dev, err := remoteNativeSite("http://127.0.0.1:39003", "http://127.0.0.1:39004", "127.0.0.1:39004", "host_one", open)
	if err != nil {
		t.Fatal(err)
	}
	docker, err := remoteNativeSite("http://127.0.0.1:39003", "", "127.0.0.1:18889", "docker-host", open)
	if err != nil || docker.Origin == "" || docker.Listen == "127.0.0.1:18889" {
		t.Fatalf("remote-only Docker requires an independent loopback site: %+v, %v", docker, err)
	}
	other, err := remoteNativeSite("http://127.0.0.1:39003", "http://127.0.0.1:39004", "127.0.0.1:39004", "host-two", open)
	if err != nil {
		t.Fatal(err)
	}
	if dev.Origin != "http://127.0.0.1:46610" || dev.Listen != "127.0.0.1:46610" {
		t.Fatalf("loopback site = %+v", dev)
	}
	if other.Origin == dev.Origin || other.Listen == dev.Listen {
		t.Fatalf("loopback hosts do not get their own origin: %q %q", dev.Origin, other.Origin)
	}
	if _, err := remoteNativeSite(public, "https://magpie.example/path", "127.0.0.1:39004", "host_one", open); err == nil {
		t.Fatal("non-origin URL path was accepted")
	}
}
