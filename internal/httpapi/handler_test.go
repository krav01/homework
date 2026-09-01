package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type memoryStore struct {
	mu   sync.Mutex
	data map[string]map[string]int64
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: make(map[string]map[string]int64)}
}

func (s *memoryStore) Add(user, product string, amount int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[user] == nil {
		s.data[user] = make(map[string]int64)
	}
	s.data[user][product] += amount
	return s.productAmount(product), nil
}

func (s *memoryStore) ProductAmount(product string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.productAmount(product)
}

func (s *memoryStore) productAmount(product string) int64 {
	var total int64
	for _, products := range s.data {
		total += products[product]
	}
	return total
}

func (s *memoryStore) DeleteProduct(product string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, products := range s.data {
		if _, ok := products[product]; ok {
			delete(products, product)
			found = true
		}
	}
	return found, nil
}

func TestHandler_ProductLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler(newMemoryStore())
	body := bytes.NewBufferString(`{"user_id":"loki","product_name":"apple","amount":2}`)
	writeRequest := httptest.NewRequest(http.MethodPost, "/write", body)
	writeRequest.Header.Set("Content-Type", "application/json")
	writeResponse := httptest.NewRecorder()
	handler.ServeHTTP(writeResponse, writeRequest)
	if writeResponse.Code != http.StatusCreated {
		t.Fatalf("POST /write status = %d, want %d; body=%s", writeResponse.Code, http.StatusCreated, writeResponse.Body.String())
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/get_product_amount?product_name=apple", nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResponse.Code, http.StatusOK)
	}
	var amount struct {
		Amount int64 `json:"amount"`
	}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &amount); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if amount.Amount != 2 {
		t.Fatalf("GET amount = %d, want 2", amount.Amount)
	}

	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/delete_product?product_name=apple", nil))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want %d", deleteResponse.Code, http.StatusNoContent)
	}
}

func TestHandler_WriteAcceptsFormParameters(t *testing.T) {
	t.Parallel()

	handler := NewHandler(newMemoryStore())
	request := httptest.NewRequest(http.MethodPost, "/write?user_id=thor&product_name=beer&amount=3", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
}

func TestHandler_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "uppercase product", method: http.MethodGet, target: "/get_product_amount?product_name=Coffee"},
		{name: "zero amount", method: http.MethodPost, target: "/write", body: `{"user_id":"loki","product_name":"apple","amount":0}`},
		{name: "unknown JSON field", method: http.MethodPost, target: "/write", body: `{"user_id":"loki","product_name":"apple","amount":1,"extra":true}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.target, bytes.NewBufferString(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			NewHandler(newMemoryStore()).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
}

func TestHandler_WriteBodyValidation(t *testing.T) {
	const query = "/write?user_id=alice&product_name=apple&amount=1"
	for _, test := range []struct {
		name, target, contentType, body string
		want                            int
	}{
		{"malformed form", query, "application/x-www-form-urlencoded", "amount=%", 400},
		{"null JSON", query, "application/json", "null", 400},
		{"empty JSON", query, "application/json", "", 400},
		{"JSON does not inherit query", query, "application/json", `{"amount":2}`, 400},
		{"array JSON", query, "application/json", "[]", 400},
		{"malformed JSON", query, "application/json", "{", 400},
		{"multiple JSON values", query, "application/json", `{"user_id":"alice","product_name":"apple","amount":1} {}`, 400},
		{"oversize JSON", query, "application/json", `{"user_id":"` + strings.Repeat("a", maxBodyBytes) + `"}`, 413},
		{"oversize form", query, "application/x-www-form-urlencoded", "user_id=" + strings.Repeat("a", maxBodyBytes), 413},
		{"unsupported content type", query, "text/plain", "ignored", 415},
		{"missing content type", query, "", "ignored", 415},
		{"malformed content type", query, "application/json; broken", `{}`, 415},
		{"malformed query", "/write?user_id=alice&product_name=apple&amount=1&bad=%", "", "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newMemoryStore()
			request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			NewHandler(s).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
			if amount := s.ProductAmount("apple"); amount != 0 {
				t.Fatalf("rejected request wrote %d", amount)
			}
		})
	}
}

func TestHandler_WriteParameterPrecedence(t *testing.T) {
	for _, test := range []struct{ name, contentType, body string }{
		{"JSON only", "application/json", `{"user_id":"alice","product_name":"apple","amount":2}`},
		{"form overrides query", "application/x-www-form-urlencoded", "user_id=alice&product_name=apple&amount=2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newMemoryStore()
			request := httptest.NewRequest(http.MethodPost, "/write?user_id=bob&product_name=banana&amount=9", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			NewHandler(s).ServeHTTP(response, request)
			if response.Code != 201 || s.ProductAmount("apple") != 2 || s.ProductAmount("banana") != 0 {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}
