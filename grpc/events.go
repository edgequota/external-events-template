package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	eventsv1 "github.com/edgequota/edgequota-go/gen/grpc/edgequota/events/v1"
)

const maxStoredEvents = 10000

type EventStats struct {
	TotalReceived int64 `json:"total_received"`
	TotalAllowed  int64 `json:"total_allowed"`
	TotalDenied   int64 `json:"total_denied"`
	StoredEvents  int   `json:"stored_events"`
}

type EventService struct {
	eventsv1.UnimplementedEventServiceServer

	logger *slog.Logger

	mu     sync.RWMutex
	events []*eventsv1.UsageEvent

	totalReceived atomic.Int64
	totalAllowed  atomic.Int64
	totalDenied   atomic.Int64
}

func NewEventService(logger *slog.Logger) *EventService {
	return &EventService{
		logger: logger,
		events: make([]*eventsv1.UsageEvent, 0, 1024),
	}
}

func (s *EventService) PublishEvents(_ context.Context, req *eventsv1.PublishEventsRequest) (*eventsv1.PublishEventsResponse, error) {
	batch := req.GetEvents()
	count := int64(len(batch))

	var allowed, denied int64
	for _, ev := range batch {
		if ev.GetAllowed() {
			allowed++
		} else {
			denied++
		}
	}

	s.store(batch)
	s.totalReceived.Add(count)
	s.totalAllowed.Add(allowed)
	s.totalDenied.Add(denied)

	s.logger.Info("events received", "count", count, "allowed", allowed, "denied", denied)
	return &eventsv1.PublishEventsResponse{Accepted: count}, nil
}

func (s *EventService) HandleListEvents(w http.ResponseWriter, r *http.Request) {
	tenantFilter := r.URL.Query().Get("tenant_key")
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	s.mu.RLock()
	result := make([]*eventsv1.UsageEvent, 0, min(limit, len(s.events)))
	for i := len(s.events) - 1; i >= 0 && len(result) < limit; i-- {
		ev := s.events[i]
		if tenantFilter != "" && ev.GetTenantKey() != tenantFilter {
			continue
		}
		result = append(result, ev)
	}
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, result)
}

func (s *EventService) HandleStats(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	n := len(s.events)
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, EventStats{
		TotalReceived: s.totalReceived.Load(),
		TotalAllowed:  s.totalAllowed.Load(),
		TotalDenied:   s.totalDenied.Load(),
		StoredEvents:  n,
	})
}

func (s *EventService) HandleClearEvents(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.events = s.events[:0]
	s.mu.Unlock()
	s.totalReceived.Store(0)
	s.totalAllowed.Store(0)
	s.totalDenied.Store(0)

	s.logger.Info("events cleared")
	w.WriteHeader(http.StatusNoContent)
}

func (s *EventService) store(batch []*eventsv1.UsageEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, batch...)
	if len(s.events) > maxStoredEvents {
		excess := len(s.events) - maxStoredEvents
		s.events = s.events[excess:]
	}
}

func (s *EventService) StoredEvents() []*eventsv1.UsageEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*eventsv1.UsageEvent, len(s.events))
	copy(out, s.events)
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
