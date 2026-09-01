// Package httpapi exposes the Sunday groceries HTTP API.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strconv"

	"github.com/krav01/homework/internal/store"
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
		var body requestBody
		request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
		mediaType := ""
		if value := request.Header.Get("Content-Type"); value != "" {
			parsed, _, err := mime.ParseMediaType(value)
			if err != nil {
				writeError(writer, http.StatusUnsupportedMediaType, errors.New("invalid content type"))
				return
			}
			mediaType = parsed
		}
		switch mediaType {
		case "application/json":
			// JSON is the complete payload. URL parameters never fill missing fields.
			decoder := json.NewDecoder(request.Body)
			decoder.DisallowUnknownFields()
			var decoded *requestBody
			if err := decoder.Decode(&decoded); err != nil {
				writeInputError(writer, err)
				return
			}
			if decoded == nil {
				writeError(writer, http.StatusBadRequest, errors.New("request body must be a JSON object"))
				return
			}
			if err := ensureSingleJSONValue(decoder); err != nil {
				writeInputError(writer, err)
				return
			}
			body = *decoded
		case "", "application/x-www-form-urlencoded":
			if mediaType == "" && request.ContentLength != 0 {
				writeError(writer, http.StatusUnsupportedMediaType, errors.New("content type is required for a request body"))
				return
			}
			if err := request.ParseForm(); err != nil {
				writeInputError(writer, err)
				return
			}
			// ParseForm gives form-body values precedence over query parameters.
			body.UserID = request.Form.Get("user_id")
			body.ProductName = request.Form.Get("product_name")
			amount, err := strconv.ParseInt(request.Form.Get("amount"), 10, 64)
			if err != nil {
				writeError(writer, http.StatusBadRequest, errors.New("amount must be an integer"))
				return
			}
			body.Amount = amount
		default:
			writeError(writer, http.StatusUnsupportedMediaType, errors.New("unsupported content type"))
			return
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
			slog.ErrorContext(request.Context(), "persist product", "error", err)
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
			slog.ErrorContext(request.Context(), "persist product deletion", "error", err)
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
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return errors.New("request body must contain one JSON object")
}

func writeInputError(writer http.ResponseWriter, err error) {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		writeError(writer, http.StatusRequestEntityTooLarge, errors.New("request body exceeds 1 MiB"))
		return
	}
	writeError(writer, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
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
