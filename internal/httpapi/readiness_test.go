package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type readinessMemoryStore struct {
	*memoryStore
	readyErr error
}

func (s *readinessMemoryStore) Ready() error {
	return s.readyErr
}

func TestHandler_ReadinessReflectsStoreState(t *testing.T) {
	t.Parallel()

	store := &readinessMemoryStore{memoryStore: newMemoryStore()}
	handler := NewHandler(store)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", response.Code, http.StatusOK)
	}

	store.readyErr = errors.New("durability unknown")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded readiness status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d", response.Code, http.StatusOK)
	}
}
