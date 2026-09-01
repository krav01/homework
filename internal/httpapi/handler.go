// Package httpapi exposes the Sunday groceries HTTP API.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/codex/sunday-system/internal/store"
)

const maxBodyBytes = 1 << 20

var lowercaseName = regexp.MustCompile(`^[a-z]+$`)

// GroceryStore is the persistence boundary used by the HTTP layer.
type GroceryStore interface {
	Add(user, product string, amount int64) (int64, error)
	ProductAmount(product string) int64
	DeleteProduct(product string) (bool, error)
}

// NewHandler constructs the Sunday API routes.
func NewHandler(groceries GroceryStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /get_product_amount", getProductAmount(groceries))
	mux.HandleFunc("POST /write", writeProduct(groceries))
	mux.HandleFunc("DELETE /delete_product", deleteProduct(groceries))
	return mux
}

func getProductAmount(groceries GroceryStore) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		product := request.URL.Query().Get("product_name")
		if err := validateName("product_name", product); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"product_name": product,
			"amount":       groceries.ProductAmount(product),
		})
	}
}

func writeProduct(groceries GroceryStore) http.HandlerFunc {
	type requestBody struct {
		UserID      string `json:"user_id"`
		ProductName string `json:"product_name"`
		Amount      int64  `json:"amount"`
	}

	return func(writer http.ResponseWriter, request *http.Request) {
		body := requestBody{
			UserID:      request.URL.Query().Get("user_id"),
			ProductName: request.URL.Query().Get("product_name"),
		}
		if value := request.URL.Query().Get("amount"); value != "" {
			amount, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, errors.New("amount must be an integer"))
				return
			}
			body.Amount = amount
		}

		if strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
			request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
			decoder := json.NewDecoder(request.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				writeError(writer, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
				return
			}
			if err := ensureSingleJSONValue(decoder); err != nil {
				writeError(writer, http.StatusBadRequest, err)
				return
			}
		} else if err := request.ParseForm(); err == nil {
			if value := request.Form.Get("user_id"); value != "" {
				body.UserID = value
			}
			if value := request.Form.Get("product_name"); value != "" {
				body.ProductName = value
			}
			if value := request.Form.Get("amount"); value != "" {
				amount, parseErr := strconv.ParseInt(value, 10, 64)
				if parseErr != nil {
					writeError(writer, http.StatusBadRequest, errors.New("amount must be an integer"))
					return
				}
				body.Amount = amount
			}
		}

		if err := validateName("user_id", body.UserID); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if err := validateName("product_name", body.ProductName); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}
		if body.Amount <= 0 {
			writeError(writer, http.StatusBadRequest, errors.New("amount must be positive"))
			return
		}

		total, err := groceries.Add(body.UserID, body.ProductName, body.Amount)
		if errors.Is(err, store.ErrAmountOverflow) {
			writeError(writer, http.StatusUnprocessableEntity, err)
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, errors.New("persist product"))
			return
		}
		writeJSON(writer, http.StatusCreated, map[string]any{
			"user_id":      body.UserID,
			"product_name": body.ProductName,
			"amount":       body.Amount,
			"total":        total,
		})
	}
}

func deleteProduct(groceries GroceryStore) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		product := request.URL.Query().Get("product_name")
		if product == "" {
			if err := request.ParseForm(); err == nil {
				product = request.Form.Get("product_name")
			}
		}
		if err := validateName("product_name", product); err != nil {
			writeError(writer, http.StatusBadRequest, err)
			return
		}

		deleted, err := groceries.DeleteProduct(product)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, errors.New("persist product deletion"))
			return
		}
		if !deleted {
			writeError(writer, http.StatusNotFound, errors.New("product not found"))
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}
}

func validateName(field, value string) error {
	if !lowercaseName.MatchString(value) {
		return fmt.Errorf("%s must contain lowercase letters only", field)
	}
	return nil
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}
