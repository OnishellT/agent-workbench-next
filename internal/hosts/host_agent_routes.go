package hosts

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/crypto/ssh"
)

func (s *Server) hostAgentStatusRoute(w http.ResponseWriter, r *http.Request) {
	if s.SSH == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("servicio SSH no disponible"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var result hostAgentStatus
	err := s.SSH.SSHOperation(ctx, r.PathValue("id"), func(opCtx context.Context, client *ssh.Client, identity Identity) error {
		state, err := inspectRemoteHostAgent(opCtx, client, identity)
		result = state.status
		if err == nil {
			s.agentCache.put(identity, state)
		} else {
			s.agentCache.invalidate(identity.HostID)
		}
		clear(state.token)
		return err
	})
	if err != nil {
		writeHostAgentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) hostAgentInstallRoute(w http.ResponseWriter, r *http.Request) {
	if s.SSH == nil {
		fail(w, http.StatusServiceUnavailable, errors.New("servicio SSH no disponible"))
		return
	}
	var input hostAgentInstallInput
	if err := decodeStrictRequest(w, r, hostAgentRequestLimit, &input); err != nil {
		fail(w, http.StatusBadRequest, errors.New("solicitud de instalación inválida"))
		return
	}
	if input.Revision < 1 {
		fail(w, http.StatusBadRequest, errors.New("revisión del host obligatoria"))
		return
	}
	// No route deadline here: the revision CAS stays first, and the install
	// deadline is sized from the payloads once the binaries are open. A fixed
	// 20s truncated slow-link uploads (~85MB workbench+magpie) mid-copy with no
	// owner to attribute the partial state, and the retry met an
	// unattributable 409. Dial timeouts live in the SSH dialer itself.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var result hostAgentStatus
	created := false
	err := s.SSH.SSHOperation(ctx, r.PathValue("id"), func(opCtx context.Context, client *ssh.Client, identity Identity) error {
		if identity.Revision != input.Revision {
			return errHostAgentCAS
		}
		binary, err := openHostAgentBinary()
		if err != nil {
			return errors.Join(errHostAgentReleaseUnavailable, err)
		}
		defer binary.file.Close()
		magpieBinary, err := openNativeMagpieBinary()
		if err != nil {
			return errors.Join(errHostAgentReleaseUnavailable, err)
		}
		defer magpieBinary.file.Close()
		installCtx, stop := context.WithTimeout(opCtx, hostAgentInstallTimeout(binary.size+magpieBinary.size))
		defer stop()
		s.agentCache.invalidate(identity.HostID)
		state, wasCreated, err := installRemoteHostAgent(installCtx, client, identity, binary, magpieBinary)
		result, created = state.status, wasCreated
		return err
	})
	if err != nil {
		writeHostAgentError(w, err)
		return
	}
	status := http.StatusOK
	if created && result.Ready {
		status = http.StatusCreated
	} else if !result.Ready {
		status = http.StatusAccepted
	}
	writeJSON(w, status, result)
}
