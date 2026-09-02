package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestRequestLoggerBoundsUserInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		target     string
		wantMethod string
		wantPath   string
	}{
		{name: "health", method: "GET", target: "/healthz", wantMethod: "GET", wantPath: "/healthz"},
		{
			name: "read", method: "GET", target: "/get_product_amount",
			wantMethod: "GET", wantPath: "/get_product_amount",
		},
		{
			name: "write hides query", method: "POST", target: "/write?token=secret",
			wantMethod: "POST", wantPath: "/write",
		},
		{
			name: "delete", method: "DELETE", target: "/delete_product",
			wantMethod: "DELETE", wantPath: "/delete_product",
		},
		{name: "newline", method: "GET", target: "/secret%0aFORGED", wantMethod: "GET", wantPath: "<unmatched>"},
		{name: "quotes", method: "GET", target: "/%22secret%22", wantMethod: "GET", wantPath: "<unmatched>"},
		{
			name: "large input", method: strings.Repeat("secret", 1000),
			target: "/" + strings.Repeat("secret", 1000), wantMethod: "OTHER", wantPath: "<unmatched>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			handler := requestLogger(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, tt.target, nil)
			request.Method = tt.method
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("handler status = %d, want 204", response.Code)
			}
			got := logs.String()
			containsInput := strings.Contains(got, "secret")
			invalidRecordCount := strings.Count(got, "\n") != 1
			oversized := len(got) > 400
			if containsInput || invalidRecordCount || oversized {
				t.Fatalf("request log contains raw input or unexpected records: %q", got)
			}
			var entry struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("decode request log: %v", err)
			}
			if entry.Method != tt.wantMethod || entry.Path != tt.wantPath {
				t.Fatalf(
					"labels = (%q, %q), want (%q, %q)",
					entry.Method,
					entry.Path,
					tt.wantMethod,
					tt.wantPath,
				)
			}
		})
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
