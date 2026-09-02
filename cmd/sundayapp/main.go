package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krav01/homework/internal/httpapi"
	"github.com/krav01/homework/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("run Sunday API", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) (returnErr error) {
	dataPath := envOrDefault("DATA_PATH", "/data/sunday.json")
	address := envOrDefault("HTTP_ADDR", ":8080")
	groceries, err := store.NewFileStore(dataPath)
	if err != nil {
		return fmt.Errorf("open grocery store: %w", err)
	}

	defer func() { returnErr = errors.Join(returnErr, groceries.Close()) }()

	server := &http.Server{
		Addr:              address,
		Handler:           requestLogger(logger, httpapi.NewHandler(groceries)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("starting Sunday API", "address", address, "dataPath", dataPath)
	return runServer(ctx, server)
}

type serverRunner interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

func runServer(ctx context.Context, server serverRunner) error {
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP: %w", err)
	}

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", err)
		}
		return nil
	case <-shutdownCtx.Done():
		return fmt.Errorf("wait for HTTP shutdown: %w", shutdownCtx.Err())
	}
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		details := &responseDetailsWriter{ResponseWriter: writer}
		next.ServeHTTP(details, request)
		if details.status == 0 {
			details.status = http.StatusOK
		}
		logger.Info("HTTP request",
			"method", logMethod(request.Method),
			"path", logPath(request.URL.Path),
			"status", details.status,
			"bytes", details.bytes,
			"duration", time.Since(started),
		)
	})
}

// Log fixed labels rather than arbitrary client input. JSON encoding protects
// record boundaries; the allowlist also bounds log size and avoids leaking URLs.
func logMethod(method string) string {
	switch method {
	case http.MethodGet:
		return http.MethodGet
	case http.MethodHead:
		return http.MethodHead
	case http.MethodPost:
		return http.MethodPost
	case http.MethodPut:
		return http.MethodPut
	case http.MethodPatch:
		return http.MethodPatch
	case http.MethodDelete:
		return http.MethodDelete
	case http.MethodOptions:
		return http.MethodOptions
	case http.MethodConnect:
		return http.MethodConnect
	case http.MethodTrace:
		return http.MethodTrace
	default:
		return "OTHER"
	}
}

func logPath(path string) string {
	switch path {
	case "/healthz":
		return "/healthz"
	case "/get_product_amount":
		return "/get_product_amount"
	case "/write":
		return "/write"
	case "/delete_product":
		return "/delete_product"
	default:
		return "<unmatched>"
	}
}

type responseDetailsWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseDetailsWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseDetailsWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	written, err := w.ResponseWriter.Write(body)
	w.bytes += written
	return written, err
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
