package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"workbench/internal/app"
	"workbench/internal/config"
	"workbench/internal/foundry"
	"workbench/internal/foundryloop"
	"workbench/internal/harness"
	"workbench/internal/hostagent"
	"workbench/internal/hosts"
	"workbench/internal/memu"
	"workbench/internal/models"
	"workbench/internal/settings"
	"workbench/web"
)

var version = "development"

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) (result error) {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("Agent Workbench " + version)
		return nil
	}
	if len(args) > 0 && args[0] == "host-agent" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return hostagent.Run(ctx, args[1:], version)
	}
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	cfg.Version = version
	drained := true
	stateLock, err := config.LockStateDir(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("lock state directory: %w", err)
	}
	defer func() {
		if drained {
			result = errors.Join(result, stateLock.Close())
		}
	}()
	prefs, err := settings.Open(cfg.StateDir)
	if err != nil {
		return fmt.Errorf("open settings: %w", err)
	}
	// Keep the prior DB location so existing SSH pins and encrypted credentials stay available.
	registry, err := hosts.NewModule(filepath.Join(cfg.StateDir, "atlas"))
	if err != nil {
		return fmt.Errorf("open hosts: %w", err)
	}

	defer func() {
		if drained {
			result = errors.Join(result, registry.Close())
		}
	}()

	var gateway *models.Module
	if os.Getenv("WORKBENCH_REMOTE_ACTOR_ONLY") == "true" {
		gateway = models.NewRemoteOnly()
	} else {
		nativeHome, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return fmt.Errorf("native user home: %w", homeErr)
		}
		gateway, err = models.New(filepath.Join(cfg.StateDir, "models"), cfg.PublicURL, cfg.Listen, nativeHome)
		if err != nil {
			return fmt.Errorf("open models: %w", err)
		}
	}
	defer func() {
		if !drained {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result = errors.Join(result, gateway.Close(ctx))
	}()

	engines, err := harness.New(harness.Options{StateDir: cfg.StateDir, Router: gateway})
	if err != nil {
		return fmt.Errorf("open engine launcher: %w", err)
	}
	projectRoots := func() []string {
		roots := []string{}
		for _, project := range prefs.Snapshot().Projects {
			roots = append(roots, project.Path)
		}
		return roots
	}
	notebooks, err := foundry.New(foundry.Options{StateDir: cfg.StateDir, HostScope: registry.HostsService(), AgentEngines: engines, ProjectRoots: projectRoots, RemoteMemuRunner: registry.RunMemuJob})
	if err != nil {
		return fmt.Errorf("open Foundry: %w", err)
	}
	registry.SetHistoryImporter(notebooks)
	if err := notebooks.EnsureWorkSource(context.Background(), filepath.Join(cfg.StateDir, "conversations")); err != nil {
		return fmt.Errorf("register the work source: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := notebooks.Close(ctx)
		drained = err == nil && drained
		result = errors.Join(result, err)
	}()
	// Foundry (Python, loopback) starts on the first /foundry request, not here: a missing
	// interpreter must not keep the dashboard from opening.
	foundryLoop, err := foundryloop.New(foundryloop.Options{
		StateDir: cfg.StateDir,
		DataDir:  strings.TrimSpace(os.Getenv("WORKBENCH_FOUNDRY_DATA")),
		Python:   strings.TrimSpace(os.Getenv("WORKBENCH_FOUNDRY_PYTHON")),
	})
	if err != nil {
		return fmt.Errorf("configure Foundry: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := foundryLoop.Close(ctx)
		drained = err == nil && drained
		result = errors.Join(result, err)
	}()
	var application *app.Server
	defer func() {
		if application != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			err := application.Close(ctx)
			drained = err == nil && drained
			result = errors.Join(result, err)
		}
	}()

	sites := &siteSet{handler: func() http.Handler { return application.NativeSiteHandler("") }}
	serveNativeSite := sites.serve

	var nativeSites []app.NativeSite
	if gateway.SiteAddr() != "" {
		nativeSites = []app.NativeSite{{HostID: "local", Listen: gateway.SiteAddr(), Origin: gateway.SiteOrigin(), Handler: gateway.SiteHandler()}}
	}
	artifactHandler := memu.New(cfg.StateDir, projectRoots, notebooks.MemuEvidenceAuthorized)
	artifactHandler.AfterRollback = notebooks.RestoreMemuIndex
	conversationHandler := notebooks.Handler()
	learningHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, action := range []string{"review", "adopt", "rollback", "adoptions"} {
			if r.URL.Path == "/api/foundry/memu/"+action {
				http.StripPrefix("/api/foundry/memu", artifactHandler).ServeHTTP(w, r)
				return
			}
		}
		conversationHandler.ServeHTTP(w, r)
	})
	remoteBaseOrigin, remoteSiteListen := gateway.SiteOrigin(), gateway.SiteAddr()
	if remoteSiteListen == "" {
		remoteBaseOrigin = strings.TrimSpace(os.Getenv("WORKBENCH_MAGPIE_ORIGIN"))
		remoteSiteListen = "127.0.0.1:18889"
	}
	// A failed bind is retried on the next request; only a successful one is remembered.
	var remoteSiteMu sync.Mutex
	remoteSiteServed := false
	application, err = app.New(cfg, web.Assets, app.Modules{
		Settings: prefs, Models: gateway.Handler(),
		AgentModels: gateway.AgentHandler(),
		NativeSites: nativeSites,
		NativeSiteResolver: func(ctx context.Context, hostID string) (app.NativeSite, error) {
			if err := registry.NativeMagpieReady(ctx, hostID); err != nil {
				return app.NativeSite{}, err
			}
			site, err := remoteNativeSite(cfg.PublicURL, remoteBaseOrigin, remoteSiteListen, hostID, serveNativeSite)
			if err != nil {
				return app.NativeSite{}, err
			}
			if gateway.SiteAddr() == "" && site.Listen == remoteSiteListen {
				remoteSiteMu.Lock()
				if !remoteSiteServed {
					if _, err := serveNativeSite(remoteSiteListen); err != nil {
						remoteSiteMu.Unlock()
						return app.NativeSite{}, err
					}
					remoteSiteServed = true
				}
				remoteSiteMu.Unlock()
			}
			site.Handler = registry.NativeMagpieSiteHandler(hostID)
			return site, nil
		},
		Hosts:             registry.Handler(),
		Foundry:           learningHandler,
		FoundryLoop:       foundryLoop.Handler(),
		ModuleDiagnostics: gateway.Diagnostics,
	})
	if err != nil {
		return fmt.Errorf("open application: %w", err)
	}

	var siteListeners []net.Listener
	for _, site := range application.NativeSiteListeners() {
		listener, err := net.Listen("tcp", site.Listen)
		if err != nil {
			for _, opened := range siteListeners {
				_ = opened.Close()
			}
			return fmt.Errorf("listen on native Magpie site %s: %w", site.Listen, err)
		}
		siteListeners = append(siteListeners, listener)
		sites.add(newHTTPServer(site.Listen, application.NativeSiteHandler("")))
	}
	httpServer := newHTTPServer(cfg.Listen, application.Handler())
	startupSites := sites.all()
	servers := append(append([]*http.Server{}, startupSites...), httpServer)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, server := range append(sites.all(), httpServer) {
			_ = server.Shutdown(ctx)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	registry.StartHistoryAutoSync(ctx, 0)
	defer registry.StopHistoryAutoSync()
	finished := make(chan error, len(servers))
	for i, server := range startupSites {
		listener := siteListeners[i]
		go func() { finished <- server.Serve(listener) }()
	}
	go func() { finished <- httpServer.ListenAndServe() }()
	log.Printf("Agent Workbench %s listening on %s", version, cfg.Listen)
	select {
	case err := <-finished:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// Cancel in-flight request contexts first so streams end instead of holding Shutdown.
		go func() { _ = application.Close(shutdown) }()
		var shutdownErr error
		for _, server := range append(sites.all(), httpServer) {
			shutdownErr = errors.Join(shutdownErr, server.Shutdown(shutdown))
		}
		for range servers {
			if err := <-finished; !errors.Is(err, http.ErrServerClosed) {
				shutdownErr = errors.Join(shutdownErr, err)
			}
		}
		return shutdownErr
	}
}
