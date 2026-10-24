package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// remoteStderrLimit bounds the stderr kept from a failed remote command. It is
// only ever logged server-side after redaction; it never reaches the UI.
const remoteStderrLimit = 2048

// Failure categories surfaced to the UI instead of raw remote stderr.
const (
	remoteFailStart   = "start"
	remoteFailOutput  = "output"
	remoteFailExit    = "exit"
	remoteFailTimeout = "timeout"
	remoteFailCancel  = "canceled"
)

// remoteCommandError is a categorised remote failure. Error() keeps the short
// generic message; the redacted stderr was logged server-side only.
type remoteCommandError struct {
	Category string
	ExitCode int
	msg      string
	cause    error
}

func (e *remoteCommandError) Error() string { return e.msg }
func (e *remoteCommandError) Unwrap() error { return e.cause }

// describe is the UI-safe sentence for the category (no remote text).
func (e *remoteCommandError) describe() string {
	switch e.Category {
	case remoteFailExit:
		return fmt.Sprintf("el comando remoto terminó con error (código %d); revisa el registro del servidor", e.ExitCode)
	case remoteFailOutput:
		return "el comando remoto devolvió una respuesta inválida"
	case remoteFailStart:
		return "no se pudo iniciar el comando remoto"
	case remoteFailTimeout:
		return "el comando remoto superó el tiempo de espera"
	case remoteFailCancel:
		return "el comando remoto se canceló"
	}
	return "el comando remoto falló"
}

// boundedBuffer keeps at most max bytes and silently drops the rest, so a
// noisy remote command cannot grow server memory.
type boundedBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - len(b.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf...)
}

var (
	secretAssignment = regexp.MustCompile(`(?i)(pass(?:word|wd)?|token|secret|api[_-]?key|authorization|credential|bearer)(\s*[=:]\s*|\s+)\S+`)
	secretURLUser    = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)
	secretPEMBlock   = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(-----END [A-Z ]*PRIVATE KEY-----|$)`)
	secretLongToken  = regexp.MustCompile(`[A-Za-z0-9+/_=-]{32,}`)
)

// redactRemoteText removes credential-like content from remote output before
// it is logged, and flattens control characters so it cannot forge log lines.
func redactRemoteText(text string) string {
	if len(text) > remoteStderrLimit {
		text = text[:remoteStderrLimit]
	}
	text = secretPEMBlock.ReplaceAllString(text, "[redacted-key]")
	text = secretURLUser.ReplaceAllString(text, "${1}[redacted]@")
	text = secretAssignment.ReplaceAllString(text, "${1}${2}[redacted]")
	text = secretLongToken.ReplaceAllString(text, "[redacted]")
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(text))
}

func newRemoteCommandError(ctx context.Context, category string, exit int, stderr []byte, msg string) error {
	err := &remoteCommandError{Category: category, ExitCode: exit, msg: msg}
	if ctxErr := ctx.Err(); ctxErr != nil {
		err.cause = ctxErr
		err.Category = remoteFailCancel
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			err.Category = remoteFailTimeout
		}
	}
	log.Printf("hosts: remote ssh command failed: category=%s exit=%d stderr=%q", err.Category, exit, redactRemoteText(string(stderr)))
	return err
}

func remoteCommand(ctx context.Context, client *ssh.Client, command string) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &boundedBuffer{max: remoteStderrLimit}
	session.Stderr = stderr
	if err := session.Start(command); err != nil {
		return nil, newRemoteCommandError(ctx, remoteFailStart, 0, nil, "no se pudo ejecutar una consulta SSH remota")
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, 16385))
	if readErr != nil || len(data) > 16384 {
		return nil, newRemoteCommandError(ctx, remoteFailOutput, 0, stderr.bytes(), "respuesta SSH remota inválida")
	}
	if err := session.Wait(); err != nil {
		code := -1
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitStatus()
		}
		return nil, newRemoteCommandError(ctx, remoteFailExit, code, stderr.bytes(), "la consulta SSH remota falló")
	}
	return data, nil
}
