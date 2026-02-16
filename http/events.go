package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
)

// maxStoredEvents is the maximum number of events kept in memory.
const maxStoredEvents = 10000

// --------------------------------------------------------------------------
// EdgeQuota HTTP events protocol types
// --------------------------------------------------------------------------

// UsageEvent mirrors edgequota.events.v1.UsageEvent as JSON.
type UsageEvent struct {
	Key        string `json:"key"`
	TenantKey  string `json:"tenant_key,omitempty"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Allowed    bool   `json:"allowed"`
	Remaining  int64  `json:"remaining"`
	Limit      int64  `json:"limit"`
	Timestamp  string `json:"timestamp"`
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// PublishEventsRequest mirrors edgequota.events.v1.PublishEventsRequest as JSON.
type PublishEventsRequest struct {
	Events []UsageEvent `json:"events"`
}

// PublishEventsResponse mirrors edgequota.events.v1.PublishEventsResponse as JSON.
type PublishEventsResponse struct {
	Accepted int64 `json:"accepted"`
}

// EventStats holds aggregate counters.
type EventStats struct {
	TotalReceived int64 `json:"total_received"`
	TotalAllowed  int64 `json:"total_allowed"`
	TotalDenied   int64 `json:"total_denied"`
	StoredEvents  int   `json:"stored_events"`
}

// --------------------------------------------------------------------------
// Service
// --------------------------------------------------------------------------

// EventService implements the EdgeQuota HTTP events protocol.
// It stores received events in memory and exposes them via query endpoints.
type EventService struct {
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
// POST /events — EdgeQuota event receiver
// --------------------------------------------------------------------------

// HandlePublishEvents implements the EdgeQuota HTTP events protocol.
// EdgeQuota posts a JSON PublishEventsRequest containing a batch of usage events.
func (s *EventService) HandlePublishEvents(w http.ResponseWriter, r *http.Request) {
	var req PublishEventsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	count := int64(len(req.Events))
	var allowed, denied int64
	for _, ev := range req.Events {
		if ev.Allowed {
			allowed++
		} else {
			denied++
		}
	}

	s.store(req.Events)
	s.totalReceived.Add(count)
	s.totalAllowed.Add(allowed)
	s.totalDenied.Add(denied)

	s.logger.Info("events received",
		"count", count,
		"allowed", allowed,
		"denied", denied)

	writeJSON(w, http.StatusOK, PublishEventsResponse{
		Accepted: count,
	})
}

// --------------------------------------------------------------------------
// GET /events — Query stored events
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

// --------------------------------------------------------------------------
// GET /events/stats — Aggregate counters
// --------------------------------------------------------------------------

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

// --------------------------------------------------------------------------
// DELETE /events — Clear stored events
// --------------------------------------------------------------------------

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
