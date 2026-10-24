// SPDX-License-Identifier: AGPL-3.0-only
package foundry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type heldRequestBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *heldRequestBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return 0, io.EOF
}
func (*heldRequestBody) Close() error { return nil }

func TestCloseDrainsHTTPHandlersBeforeClosingStore(t *testing.T) {
	module, err := New(Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	handler := module.Handler()
	body := &heldRequestBody{started: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest(http.MethodPatch, "/api/tasks/config", nil)
	request.Body = body
	response := httptest.NewRecorder()
	handlerDone := make(chan struct{})
	go func() { handler.ServeHTTP(response, request); close(handlerDone) }()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("handler did not begin reading its body")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := module.Close(ctx); !errors.Is(err, ErrShutdownUnconfirmed) {
		t.Fatalf("Close with a held handler = %v, want ErrShutdownUnconfirmed", err)
	}
	if err := module.db.Ping(); err != nil {
		t.Fatalf("store closed under live handler: %v", err)
	}

	closingResponse := httptest.NewRecorder()
	handler.ServeHTTP(closingResponse, httptest.NewRequest(http.MethodGet, "/api/tasks/config", nil))
	if closingResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("new request during shutdown = %d, want 503", closingResponse.Code)
	}

	close(body.release)
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("held handler did not drain after body release")
	}
	closeErrors := make(chan error, 2)
	go func() { closeErrors <- module.Close(context.Background()) }()
	go func() { closeErrors <- module.Close(context.Background()) }()
	for i := 0; i < 2; i++ {
		if err := <-closeErrors; err != nil {
			t.Fatalf("concurrent Close %d: %v", i, err)
		}
	}
	if err := module.db.Ping(); err == nil {
		t.Fatal("store remains open after all handlers drained and Close returned")
	}
}
