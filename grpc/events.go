package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	eventsv1 "github.com/edgequota/external-events-template/grpc/gen/edgequota/events/v1"
)

// maxStoredEvents is the maximum number of events kept in memory.
// Oldest events are dropped when the limit is reached.
const maxStoredEvents = 10000

// UsageEvent is the JSON-friendly representation of a usage event.
type UsageEvent struct {
	Key        string `json:"key"`
	TenantKey  string `json:"tenant_key,omitempty"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Allowed    bool   `json:"allowed"`
	Remaining  int64  `json:"remaining"`
	Limit      int64  `json:"limit"`
	Timestamp  string `json:"timestamp"`
	StatusCode int32  `json:"status_code"`
	RequestID  string `json:"request_id,omitempty"`
}

// EventStats holds aggregate counters.
type EventStats struct {
	TotalReceived int64 `json:"total_received"`
	TotalAllowed  int64 `json:"total_allowed"`
	TotalDenied   int64 `json:"total_denied"`
	StoredEvents  int   `json:"stored_events"`
}

// EventService implements edgequota.events.v1.EventServiceServer.
// It stores received events in memory and exposes them via an HTTP query API.
type EventService struct {
	eventsv1.UnimplementedEventServiceServer

	logger *slog.Logger

	mu     sync.RWMutex
	events []UsageEvent

	totalReceived atomic.Int64
	totalAllowed  atomic.Int64
	totalDenied   atomic.Int64
}

// NewEventService creates a new EventService.
func NewEventService(logger *slog.Logger) *EventService {
	return &EventService{
		logger: logger,
		events: make([]UsageEvent, 0, 1024),
	}
}

// --------------------------------------------------------------------------
// gRPC: edgequota.events.v1.EventService/PublishEvents
// --------------------------------------------------------------------------

// PublishEvents receives a batch of usage events from EdgeQuota.
func (s *EventService) PublishEvents(_ context.Context, req *eventsv1.PublishEventsRequest) (*eventsv1.PublishEventsResponse, error) {
	batch := req.GetEvents()
	count := int64(len(batch))

	converted := make([]UsageEvent, len(batch))
	var allowed, denied int64
	for i, ev := range batch {
		converted[i] = UsageEvent{
			Key:        ev.GetKey(),
			TenantKey:  ev.GetTenantKey(),
			Method:     ev.GetMethod(),
			Path:       ev.GetPath(),
			Allowed:    ev.GetAllowed(),
			Remaining:  ev.GetRemaining(),
			Limit:      ev.GetLimit(),
			Timestamp:  ev.GetTimestamp(),
			StatusCode: ev.GetStatusCode(),
			RequestID:  ev.GetRequestId(),
		}
		if ev.GetAllowed() {
			allowed++
		} else {
			denied++
		}
	}

	s.store(converted)
	s.totalReceived.Add(count)
	s.totalAllowed.Add(allowed)
	s.totalDenied.Add(denied)

	s.logger.Info("events received",
		"count", count,
		"allowed", allowed,
		"denied", denied)

	return &eventsv1.PublishEventsResponse{
		Accepted: count,
	}, nil
}

// --------------------------------------------------------------------------
// HTTP query API
// --------------------------------------------------------------------------

// HandleListEvents returns stored events, optionally filtered.
// Query params: tenant_key, limit (default 100).
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
	result := make([]UsageEvent, 0, min(limit, len(s.events)))
	// Iterate in reverse (newest first).
	for i := len(s.events) - 1; i >= 0 && len(result) < limit; i-- {
		ev := s.events[i]
		if tenantFilter != "" && ev.TenantKey != tenantFilter {
			continue
		}
		result = append(result, ev)
	}
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, result)
}

// HandleStats returns aggregate event counters.
func (s *EventService) HandleStats(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	stored := len(s.events)
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, EventStats{
		TotalReceived: s.totalReceived.Load(),
		TotalAllowed:  s.totalAllowed.Load(),
		TotalDenied:   s.totalDenied.Load(),
		StoredEvents:  stored,
	})
}

// HandleClearEvents removes all stored events and resets counters.
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

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func (s *EventService) store(batch []UsageEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, batch...)

	// Trim to max capacity.
	if len(s.events) > maxStoredEvents {
		excess := len(s.events) - maxStoredEvents
		s.events = s.events[excess:]
	}
}

// StoredEvents returns a copy of all stored events (for testing).
func (s *EventService) StoredEvents() []UsageEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UsageEvent, len(s.events))
	copy(out, s.events)
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
