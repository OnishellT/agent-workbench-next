package app

import (
	"net/http"
	"os"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	modules := s.moduleStates()
	status := s.appStatus(modules)
	code := http.StatusOK
	if status == "degraded" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"application":       "agent-workbench",
		"version":           s.cfg.Version,
		"status":            status,
		"modules":           modules,
		"routes_not_ported": s.routesNotPorted(),
	})
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	s.writeBootstrap(w)
}

func (s *Server) writeBootstrap(w http.ResponseWriter) {
	modules := s.moduleStates()
	values := s.settings.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"application":       "agent-workbench",
		"version":           s.cfg.Version,
		"status":            s.appStatus(modules),
		"modules":           modules,
		"routes_not_ported": s.routesNotPorted(),
		"sim_url":           s.cfg.SIMURL,
		// Hosts can only use the "agent" SSH mode when this process sees an SSH agent;
		// containers and system services usually do not.
		"ssh_agent": os.Getenv("SSH_AUTH_SOCK") != "",
		"settings": map[string]any{
			"projects": values.Projects,
		},
		"projects": values.Projects,
	})
}

func (s *Server) moduleStates() map[string]string {
	state := func(mounted bool) string {
		if mounted {
			return "mounted"
		}
		return "not_implemented"
	}
	return map[string]string{
		"ui":                 state(s.assetsOK),
		"models":             state(s.models != nil),
		"foundry":            state(s.foundry != nil),
		"foundry_loop":       state(s.foundryLoop != nil),
		"settings":           "mounted",
		"platform_hosts":     state(s.hosts != nil),
		"module_diagnostics": state(s.moduleDiagnostics != nil),
	}
}

func (s *Server) appStatus(modules map[string]string) string {
	for _, name := range []string{"ui", "models"} {
		if modules[name] != "mounted" {
			return "degraded"
		}
	}
	for _, state := range modules {
		if state != "mounted" {
			return "partial"
		}
	}
	return "ok"
}

func (s *Server) routesNotPorted() []string {
	missing := make([]string, 0, 5)
	if s.foundry == nil {
		missing = append(missing, "foundry")
	}
	if s.foundryLoop == nil {
		missing = append(missing, "foundry_loop")
	}
	if s.hosts == nil {
		missing = append(missing, "platform_hosts")
	}
	// A missing diagnostics source stays an explicit gap; a wired one is not.
	if s.moduleDiagnostics == nil {
		missing = append(missing, "module_diagnostics")
	}
	return missing
}

// diagnostics serves the authenticated report of this installation: the mount states
// bootstrap publishes plus the managed module's probed state and bounded log. Without
// a diagnostics source it answers 501 instead of a state nobody probed.
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	if s.moduleDiagnostics == nil {
		writeError(w, http.StatusNotImplemented, "module_diagnostics_not_implemented")
		return
	}
	modules := s.moduleStates()
	writeJSON(w, http.StatusOK, map[string]any{
		"application":       "agent-workbench",
		"version":           s.cfg.Version,
		"status":            s.appStatus(modules),
		"modules":           modules,
		"routes_not_ported": s.routesNotPorted(),
		"native_module":     s.moduleDiagnostics(r.Context()),
	})
}
