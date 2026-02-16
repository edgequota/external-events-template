// external-events-template/grpc demonstrates how to implement the
// EdgeQuota external events gRPC protocol (edgequota.events.v1.EventService).
//
// It exposes:
//   - A gRPC server on :50053 implementing EventService/PublishEvents.
//   - An HTTP server on :8083 with GET /events to query stored events.
//
// Usage:
//
//	go run . [-grpc-addr :50053] [-http-addr :8083]
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	eventsv1 "github.com/edgequota/external-events-template/grpc/gen/edgequota/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	grpcAddr := flag.String("grpc-addr", envOrDefault("GRPC_ADDR", ":50053"), "gRPC listen address")
	httpAddr := flag.String("http-addr", envOrDefault("HTTP_ADDR", ":8083"), "HTTP listen address (query API)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc := NewEventService(logger)

	// --- gRPC server ---
	grpcServer := grpc.NewServer()
	eventsv1.RegisterEventServiceServer(grpcServer, svc)
	reflection.Register(grpcServer)

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		logger.Error("failed to listen", "addr", *grpcAddr, "error", err)
		os.Exit(1)
	}

	go func() {
		logger.Info("gRPC server listening", "addr", *grpcAddr)
		if err := grpcServer.Serve(lis); err != nil {
			logger.Error("gRPC server error", "error", err)
		}
	}()

	// --- HTTP server (query API) ---
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", svc.HandleListEvents)
	mux.HandleFunc("GET /events/stats", svc.HandleStats)
	mux.HandleFunc("DELETE /events", svc.HandleClearEvents)

	httpServer := &http.Server{
		Addr:         *httpAddr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	go func() {
		logger.Info("HTTP server listening", "addr", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	// --- Graceful shutdown ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down...")
	grpcServer.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)

	logger.Info("stopped")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
