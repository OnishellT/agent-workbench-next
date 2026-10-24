package app

import (
	"net/http"
	"strings"
)

const agentModelsPrefix = "/_internal/models"

func (s *Server) serveAgentModels(w http.ResponseWriter, r *http.Request) {
	if !loopbackPeer(r.RemoteAddr) || !s.localHealthHost(r.Host) {
		writeError(w, http.StatusForbidden, "request_rejected")
		return
	}
	for name := range r.Header {
		name = strings.ToLower(name)
		if name == "host" || name == "origin" || name == "cookie" || name == "forwarded" || strings.HasPrefix(name, "x-forwarded-") {
			writeError(w, http.StatusForbidden, "request_rejected")
			return
		}
	}
	if !validInferenceRequest(r, s.cfg.AuthToken) {
		writeError(w, http.StatusUnauthorized, "inference_bearer_required")
		return
	}
	if s.agentModels == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_models_not_configured")
		return
	}
	s.agentModels.ServeHTTP(w, r)
}
