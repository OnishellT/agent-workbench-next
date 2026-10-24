package hosts

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
)

// operationMessages are the per-surface texts of writeOperationError.
type operationMessages struct {
	timeout     string
	unavailable string
}

var (
	hostAgentMessages  = operationMessages{timeout: "tiempo de espera del host-agent agotado", unavailable: "operación SSH del host-agent no disponible"}
	dependencyMessages = operationMessages{timeout: "tiempo de espera de la operación agotado", unavailable: "operación SSH de dependencias no disponible"}
)

// writeOperationError maps SSH operation failures to HTTP for the host-agent and
// dependency surfaces. Remote stderr never reaches the client: a failed remote
// command is reported by category only (the redacted text is logged server-side).
func writeOperationError(w http.ResponseWriter, err error, msgs operationMessages) {
	var remote *remoteCommandError
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, os.ErrNotExist):
		fail(w, http.StatusNotFound, errors.New("host no encontrado"))
	case errors.Is(err, errHostAgentCallerAuth):
		fail(w, http.StatusUnauthorized, errors.New("autorización del host-agent no válida"))
	case errors.Is(err, errHostAgentReleaseUnavailable):
		fail(w, http.StatusServiceUnavailable, errors.New("release binary unavailable"))
	case errors.Is(err, errHostAgentNotReady):
		fail(w, http.StatusServiceUnavailable, errors.New("host-agent instalado pero no listo"))
	case errors.Is(err, errHostAgentCAS), errors.Is(err, errHostAgentConflict), errors.Is(err, errHostAgentInstalling),
		errors.Is(err, errDependencyConflict), errors.Is(err, errDependencyBusy),
		errors.Is(err, ErrPaused), errors.Is(err, ErrUntrusted), errors.Is(err, ErrIdentityChanged):
		fail(w, http.StatusConflict, errors.New(err.Error()))
	case errors.Is(err, context.Canceled):
		fail(w, http.StatusRequestTimeout, errors.New("operación cancelada"))
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, errors.New(msgs.timeout))
	case errors.As(err, &remote):
		fail(w, http.StatusBadGateway, errors.New(msgs.unavailable+": "+remote.describe()))
	default:
		fail(w, http.StatusBadGateway, errors.New(msgs.unavailable))
	}
}

func writeHostAgentError(w http.ResponseWriter, err error) {
	writeOperationError(w, err, hostAgentMessages)
}

func writeDependencyError(w http.ResponseWriter, err error) {
	writeOperationError(w, err, dependencyMessages)
}
