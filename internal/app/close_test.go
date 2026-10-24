package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloseRevokesAdmissionAndReportsUndrainedHandlers(t *testing.T) {
	cfg := testConfig(t)
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	release := make(chan struct{})
	hosts := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
		<-release
		_, _ = fmt.Fprint(w, "done")
	})
	server, err := New(cfg, testAssets(), testModules(t, cfg, Modules{Hosts: hosts}))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	cookie := authorize(t, handler, cfg)

	request := newRequest(http.MethodGet, cfg.PublicURL+"/api/hosts/block", "127.0.0.1:18888", nil, "")
	addCookie(request, cookie)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Hosts handler did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := server.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close while handler is blocked = %v, want deadline exceeded", err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("Close did not cancel the active request context")
	}
	shutdownProbe := newRequest(http.MethodGet, cfg.PublicURL+"/health", "127.0.0.1:18888", nil, "")
	if result := serve(handler, shutdownProbe); result.Code != http.StatusServiceUnavailable {
		t.Fatalf("app admitted a request after Close: %d %s", result.Code, result.Body.String())
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked handler did not finish after release")
	}
	if response.Code != http.StatusOK || response.Body.String() != "done" {
		t.Fatalf("drained handler response = %d %q", response.Code, response.Body.String())
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatalf("Close after drain = %v", err)
	}
}
