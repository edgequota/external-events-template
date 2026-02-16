// external-events-template/http demonstrates how to implement the
// EdgeQuota external events HTTP protocol.
//
// It exposes a single HTTP server on :8080 with:
//   - POST   /events       — EdgeQuota event receiver (JSON PublishEventsRequest).
//   - GET    /events       — Query stored events.
//   - GET    /events/stats — Aggregate counters.
//   - DELETE /events       — Clear all stored events.
//
// Usage:
//
//	go run . [-addr :8080]
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", envOrDefault("ADDR", ":8080"), "HTTP listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc := NewEventService(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", svc.HandlePublishEvents)
	mux.HandleFunc("GET /events", svc.HandleListEvents)
	mux.HandleFunc("GET /events/stats", svc.HandleStats)
	mux.HandleFunc("DELETE /events", svc.HandleClearEvents)

	server := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		logger.Info("HTTP server listening", "addr", *addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)

	logger.Info("stopped")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
