package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestRunServer_ReturnsServeError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("listen failed")
	server := newFakeServer(wantErr)
	if err := runServer(t.Context(), server); !errors.Is(err, wantErr) {
		t.Fatalf("runServer() error = %v, want %v", err, wantErr)
	}
}

func TestRunServer_GracefulShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	server := newFakeServer(http.ErrServerClosed)
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, server)
	}()
	<-server.started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runServer() error = %v, want nil", err)
	}
	if !server.wasShutdown() {
		t.Fatal("runServer() did not call Shutdown()")
	}
}

func TestRequestLogger_RecordsStatusAndBytes(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := requestLogger(logger, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
		if _, err := writer.Write([]byte("ok")); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/write", nil))

	got := logs.String()
	if !strings.Contains(got, `"status":201`) || !strings.Contains(got, `"bytes":2`) {
		t.Fatalf("request log = %s, want status and byte count", got)
	}
}

type fakeServer struct {
	serveErr     error
	started      chan struct{}
	stop         chan struct{}
	shutdownOnce sync.Once
	mu           sync.Mutex
	shutdown     bool
}

func newFakeServer(serveErr error) *fakeServer {
	return &fakeServer{
		serveErr: serveErr,
		started:  make(chan struct{}),
		stop:     make(chan struct{}),
	}
}

func (s *fakeServer) ListenAndServe() error {
	close(s.started)
	if !errors.Is(s.serveErr, http.ErrServerClosed) {
		return s.serveErr
	}
	<-s.stop
	return s.serveErr
}

func (s *fakeServer) Shutdown(context.Context) error {
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	s.shutdownOnce.Do(func() { close(s.stop) })
	return nil
}

func (s *fakeServer) wasShutdown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}
