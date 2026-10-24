package hosts

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// Server exposes the canonical host, SSH transport, helper, and remote
// conversation-history surfaces. It has no token-usage or capture endpoints.
type Server struct {
	Store           *Store
	SSH             *SSHManager
	hostService     Service
	HistoryImporter HistoryImporter
	historyStates   sync.Map
	historyAutoMu   sync.Mutex
	historyAuto     *historyAutoLoop
	agentCache      hostAgentInspectCache
	Started         time.Time
	DBPath          string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.hostRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recover() != nil {
				fail(w, http.StatusInternalServerError, errors.New("error interno"))
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) HostsHandler() http.Handler { return s.Handler() }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("solo se permite un documento JSON")
	}
	return nil
}

func truncate(value string, length int) string {
	runes := []rune(value)
	if len(runes) > length {
		return string(runes[:length]) + "…"
	}
	return value
}
