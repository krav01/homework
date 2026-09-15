package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/krav01/homework/internal/store"
)

// AddIdempotent keeps the lightweight memoryStore compatible with GroceryStore.
// Persistence-specific idempotency behavior is exercised against FileStore below.
func (s *memoryStore) AddIdempotent(user, product string, amount int64, _ string) (int64, bool, error) {
	total, err := s.Add(user, product, amount)
	return total, false, err
}

func TestHandler_IdempotentWriteReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sunday.json")
	groceries, err := store.NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	}()

	handler := NewHandler(groceries)
	body := []byte(`{"user_id":"alice","product_name":"apple","amount":3}`)

	first := httptest.NewRequest(http.MethodPost, "/write", bytes.NewReader(body))
	first.Header.Set("Content-Type", "application/json")
	first.Header.Set("Idempotency-Key", "write-123")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	if firstResponse.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("first write marked as replay")
	}

	replay := httptest.NewRequest(http.MethodPost, "/write", bytes.NewReader(body))
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("Idempotency-Key", "write-123")
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusCreated {
		t.Fatalf("replay status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	if replayResponse.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("replay response does not expose Idempotency-Replayed")
	}
	if got := groceries.ProductAmount("apple"); got != 3 {
		t.Fatalf("replay duplicated write: amount=%d want=3", got)
	}

	var response struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(replayResponse.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 3 {
		t.Fatalf("replay total=%d want=3", response.Total)
	}
}

func TestHandler_IdempotencyConflict(t *testing.T) {
	groceries, err := store.NewFileStore(filepath.Join(t.TempDir(), "sunday.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := groceries.Close(); err != nil {
			t.Error(err)
		}
	}()
	handler := NewHandler(groceries)

	write := func(amount int64) *httptest.ResponseRecorder {
		body := []byte(`{"user_id":"alice","product_name":"apple","amount":` + jsonNumber(amount) + `}`)
		request := httptest.NewRequest(http.MethodPost, "/write", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "same-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	if response := write(2); response.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", response.Code, response.Body.String())
	}
	if response := write(4); response.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d want=%d body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if got := groceries.ProductAmount("apple"); got != 2 {
		t.Fatalf("conflicting replay mutated amount=%d", got)
	}
}

func TestHandler_RejectsInvalidIdempotencyKey(t *testing.T) {
	handler := NewHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodPost, "/write", bytes.NewBufferString(`{"user_id":"alice","product_name":"apple","amount":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "invalid key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func jsonNumber(value int64) string {
	return fmt.Sprintf("%d", value)
}
