package hosts

import (
	"database/sql"
	"errors"
	"net/http"
)

type hostRecord struct {
	Host
	Status       string `json:"status"`
	Paired       bool   `json:"paired"`
	Transport    string `json:"transport"`
	SSHStatus    string `json:"ssh_status,omitempty"`
	Trusted      bool   `json:"trusted"`
	HelperReady  bool   `json:"helper_ready"`
	GatewayReady bool   `json:"gateway_ready"`
}

func (s *Server) hostGatewayList(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.Store.Hosts()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]hostRecord, 0, len(rows))
	for _, host := range rows {
		status := "unknown"
		if host.Status == "error" {
			status = "offline"
		} else if host.Status == "connected" || host.Status == "warning" {
			status = "online"
		}
		trusted := host.ObservedFingerprint == "" && sshFingerprintPattern.MatchString(host.Fingerprint)
		out = append(out, hostRecord{Host: host, Status: status, Paired: true, Transport: "ssh", SSHStatus: host.Status, Trusted: trusted})
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": out})
}

func (s *Server) wireGuardRoute(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("host_id")
	if id == "" || id == "local" {
		fail(w, http.StatusBadRequest, errors.New("selecciona un host SSH registrado"))
		return
	}
	host, err := s.Store.Host(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, errors.New("host SSH no registrado"))
		} else {
			fail(w, http.StatusInternalServerError, err)
		}
		return
	}
	result, err := inspectWireGuardRoute(r.Context(), host, runIPJSON)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
