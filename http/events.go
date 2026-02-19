package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/edgequota/edgequota-go/events"
	eventsv1http "github.com/edgequota/edgequota-go/gen/http/events/v1"
)

const maxStoredEvents = 10000

type errorResponse struct {
	Error string `json:"error"`
}

type EventStats struct {
	TotalReceived int64 `json:"total_received"`
	TotalAllowed  int64 `json:"total_allowed"`
	TotalDenied   int64 `json:"total_denied"`
	StoredEvents  int   `json:"stored_events"`
}

type EventService struct {
	logger *slog.Logger

	mu     sync.RWMutex
	stored []eventsv1http.UsageEvent

	totalReceived atomic.Int64
	totalAllowed  atomic.Int64
	totalDenied   atomic.Int64
}

func NewEventService(logger *slog.Logger) *EventService {
	return &EventService{
		logger: logger,
		stored: make([]eventsv1http.UsageEvent, 0, 1024),
	}
}

func (s *EventService) HandlePublishEvents(w http.ResponseWriter, r *http.Request) {
	var req eventsv1http.PublishEventsRequest
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

	s.logger.Info("events received", "count", count, "allowed", allowed, "denied", denied)
	resp := events.Accepted(len(req.Events))
	writeJSON(w, http.StatusOK, resp)
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
	result := make([]eventsv1http.UsageEvent, 0, min(limit, len(s.stored)))
	for i := len(s.stored) - 1; i >= 0 && len(result) < limit; i-- {
		ev := s.stored[i]
		if tenantFilter != "" {
			tk := ""
			if ev.TenantKey != nil {
				tk = *ev.TenantKey
			}
			if tk != tenantFilter {
				continue
			}
		}
		result = append(result, ev)
	}
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, result)
}

func (s *EventService) HandleStats(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	n := len(s.stored)
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
	s.stored = s.stored[:0]
	s.mu.Unlock()
	s.totalReceived.Store(0)
	s.totalAllowed.Store(0)
	s.totalDenied.Store(0)

	s.logger.Info("events cleared")
	w.WriteHeader(http.StatusNoContent)
}

func (s *EventService) store(batch []eventsv1http.UsageEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stored = append(s.stored, batch...)
	if len(s.stored) > maxStoredEvents {
		excess := len(s.stored) - maxStoredEvents
		s.stored = s.stored[excess:]
	}
}

func (s *EventService) StoredEvents() []eventsv1http.UsageEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]eventsv1http.UsageEvent, len(s.stored))
	copy(out, s.stored)
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
