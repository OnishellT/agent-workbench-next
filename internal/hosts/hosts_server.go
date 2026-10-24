package hosts

import (
	"database/sql"
	"errors"
	"net/http"
)

type trustHostInput struct {
	Revision    int64  `json:"revision"`
	Fingerprint string `json:"fingerprint"`
}

func (s *Server) hostRoutes(m *http.ServeMux) {
	s.dependencyRoutes(m)
	m.HandleFunc("GET /api/hosts", s.hostGatewayList)
	m.HandleFunc("GET /api/hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		host, err := s.Store.Host(r.PathValue("id"))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fail(w, http.StatusNotFound, errors.New("host SSH no registrado"))
			} else {
				fail(w, http.StatusInternalServerError, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, host)
	})
	m.HandleFunc("POST /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		s.saveHost(w, r, "", http.StatusCreated)
	})
	m.HandleFunc("PUT /api/hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.saveHost(w, r, r.PathValue("id"), http.StatusOK)
	})
	m.HandleFunc("POST /api/hosts/{id}/trust", func(w http.ResponseWriter, r *http.Request) {
		if s.SSH == nil {
			fail(w, http.StatusServiceUnavailable, errors.New("gestor SSH no disponible"))
			return
		}
		var input trustHostInput
		if err := decode(w, r, &input); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		host, err := s.SSH.TrustFingerprint(r.PathValue("id"), input.Revision, input.Fingerprint)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			} else if errors.Is(err, ErrIdentityChanged) {
				status = http.StatusConflict
			}
			fail(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, host)
	})
	m.HandleFunc("POST /api/hosts/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		if s.SSH == nil {
			fail(w, http.StatusServiceUnavailable, errors.New("gestor SSH no disponible"))
			return
		}
		started, err := s.SSH.Start(r.PathValue("id"))
		if err != nil {
			status := http.StatusConflict
			if errors.Is(err, sql.ErrNoRows) {
				status = http.StatusNotFound
			}
			fail(w, status, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"started": started, "host_id": r.PathValue("id")})
	})
	m.HandleFunc("GET /api/hosts/{id}/history", s.hostHistoryRoute)
	m.HandleFunc("POST /api/hosts/{id}/history", s.hostHistoryConsentRoute)
	m.HandleFunc("POST /api/hosts/{id}/history/sync", s.hostHistorySyncRoute)
	m.HandleFunc("GET /api/network/wireguard", s.wireGuardRoute)
	s.conversationRoutes(m)
	s.magpieHostRoutes(m)
}

func (s *Server) saveHost(w http.ResponseWriter, r *http.Request, pathID string, status int) {
	if s.SSH == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("gestor SSH no disponible"))
		return
	}
	var input HostInput
	if err := decode(w, r, &input); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if pathID != "" {
		if input.ID != "" && input.ID != pathID {
			fail(w, http.StatusBadRequest, errors.New("el ID de host no coincide con la ruta"))
			return
		}
		input.ID = pathID
	} else {
		input.CreateOnly = true
	}
	host, err := s.SSH.Save(input)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrHostExists) {
			status = http.StatusConflict
		}
		fail(w, status, err)
		return
	}
	if host.Enabled {
		if _, err = s.SSH.Start(host.ID); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
	}
	host, err = s.Store.Host(host.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status, host)
}
