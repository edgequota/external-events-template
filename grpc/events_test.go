package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	eventsv1 "github.com/edgequota/edgequota-go/gen/grpc/edgequota/events/v1"
)

func testService() *EventService {
	return NewEventService(slog.Default())
}

func makeEvents(allowed, denied int) []*eventsv1.UsageEvent {
	events := make([]*eventsv1.UsageEvent, 0, allowed+denied)
	for i := range allowed {
		events = append(events, &eventsv1.UsageEvent{
			Key:        "10.0.0.1",
			TenantKey:  "tenant-1",
			Method:     "GET",
			Path:       "/api/v1/data",
			Allowed:    true,
			Remaining:  int64(99 - i),
			Limit:      100,
			Timestamp:  "2026-02-16T21:00:00Z",
			StatusCode: 200,
			RequestId:  "req-allowed-" + string(rune('a'+i)),
		})
	}
	for i := range denied {
		events = append(events, &eventsv1.UsageEvent{
			Key:        "10.0.0.1",
			TenantKey:  "tenant-1",
			Method:     "POST",
			Path:       "/api/v1/data",
			Allowed:    false,
			Remaining:  0,
			Limit:      100,
			Timestamp:  "2026-02-16T21:00:01Z",
			StatusCode: 429,
			RequestId:  "req-denied-" + string(rune('a'+i)),
		})
	}
	return events
}

func TestPublishEvents_SingleBatch(t *testing.T) {
	svc := testService()
	resp, err := svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
		Events: makeEvents(3, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted != 5 {
		t.Errorf("expected accepted=5, got %d", resp.Accepted)
	}
	if svc.totalReceived.Load() != 5 {
		t.Errorf("expected totalReceived=5, got %d", svc.totalReceived.Load())
	}
	if svc.totalAllowed.Load() != 3 {
		t.Errorf("expected totalAllowed=3, got %d", svc.totalAllowed.Load())
	}
	if svc.totalDenied.Load() != 2 {
		t.Errorf("expected totalDenied=2, got %d", svc.totalDenied.Load())
	}
}

func TestPublishEvents_MultipleBatches(t *testing.T) {
	svc := testService()
	for range 3 {
		_, err := svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
			Events: makeEvents(2, 1),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if svc.totalReceived.Load() != 9 {
		t.Errorf("expected totalReceived=9, got %d", svc.totalReceived.Load())
	}
	stored := svc.StoredEvents()
	if len(stored) != 9 {
		t.Errorf("expected 9 stored events, got %d", len(stored))
	}
}

func TestPublishEvents_EmptyBatch(t *testing.T) {
	svc := testService()
	resp, err := svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
		Events: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted != 0 {
		t.Errorf("expected accepted=0, got %d", resp.Accepted)
	}
}

func TestPublishEvents_EventFieldsPreserved(t *testing.T) {
	svc := testService()
	_, err := svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
		Events: []*eventsv1.UsageEvent{
			{
				Key:        "key-1",
				TenantKey:  "tenant-42",
				Method:     "DELETE",
				Path:       "/api/v1/resource/123",
				Allowed:    true,
				Remaining:  50,
				Limit:      100,
				Timestamp:  "2026-02-16T21:30:00Z",
				StatusCode: 200,
				RequestId:  "req-xyz",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stored := svc.StoredEvents()
	if len(stored) != 1 {
		t.Fatalf("expected 1 event, got %d", len(stored))
	}
	ev := stored[0]
	if ev.GetKey() != "key-1" || ev.GetTenantKey() != "tenant-42" || ev.GetMethod() != "DELETE" ||
		ev.GetPath() != "/api/v1/resource/123" || !ev.GetAllowed() || ev.GetRemaining() != 50 ||
		ev.GetLimit() != 100 || ev.GetStatusCode() != 200 || ev.GetRequestId() != "req-xyz" {
		t.Errorf("event fields not preserved: %+v", ev)
	}
}

func TestListEvents_Empty(t *testing.T) {
	svc := testService()
	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	svc.HandleListEvents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var events []*eventsv1.UsageEvent
	if err := json.NewDecoder(w.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestListEvents_WithFilter(t *testing.T) {
	svc := testService()
	svc.store([]*eventsv1.UsageEvent{
		{Key: "k1", TenantKey: "tenant-a", Method: "GET", Path: "/a", Allowed: true, Timestamp: "t1"},
		{Key: "k2", TenantKey: "tenant-b", Method: "GET", Path: "/b", Allowed: true, Timestamp: "t2"},
		{Key: "k3", TenantKey: "tenant-a", Method: "POST", Path: "/a", Allowed: false, Timestamp: "t3"},
	})

	req := httptest.NewRequest("GET", "/events?tenant_key=tenant-a", nil)
	w := httptest.NewRecorder()
	svc.HandleListEvents(w, req)

	var events []*eventsv1.UsageEvent
	if err := json.NewDecoder(w.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for tenant-a, got %d", len(events))
	}
}

func TestListEvents_WithLimit(t *testing.T) {
	svc := testService()
	batch := make([]*eventsv1.UsageEvent, 10)
	for i := range batch {
		batch[i] = &eventsv1.UsageEvent{Key: "k", TenantKey: "t", Method: "GET", Path: "/", Allowed: true, Timestamp: "ts"}
	}
	svc.store(batch)

	req := httptest.NewRequest("GET", "/events?limit=3", nil)
	w := httptest.NewRecorder()
	svc.HandleListEvents(w, req)

	var events []*eventsv1.UsageEvent
	if err := json.NewDecoder(w.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Errorf("expected 3 events, got %d", len(events))
	}
}

func TestStats(t *testing.T) {
	svc := testService()
	svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
		Events: makeEvents(5, 3),
	})

	req := httptest.NewRequest("GET", "/events/stats", nil)
	w := httptest.NewRecorder()
	svc.HandleStats(w, req)

	var stats EventStats
	if err := json.NewDecoder(w.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalReceived != 8 || stats.TotalAllowed != 5 || stats.TotalDenied != 3 || stats.StoredEvents != 8 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestClearEvents(t *testing.T) {
	svc := testService()
	svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
		Events: makeEvents(3, 0),
	})

	req := httptest.NewRequest("DELETE", "/events", nil)
	w := httptest.NewRecorder()
	svc.HandleClearEvents(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if len(svc.StoredEvents()) != 0 {
		t.Error("expected 0 events after clear")
	}
	if svc.totalReceived.Load() != 0 {
		t.Error("expected totalReceived=0 after clear")
	}
}

func TestE2E_PublishQueryStatsClear(t *testing.T) {
	svc := testService()

	_, err := svc.PublishEvents(context.Background(), &eventsv1.PublishEventsRequest{
		Events: makeEvents(4, 1),
	})
	if err != nil {
		t.Fatal(err)
	}

	listReq := httptest.NewRequest("GET", "/events?limit=10", nil)
	listW := httptest.NewRecorder()
	svc.HandleListEvents(listW, listReq)
	var events []*eventsv1.UsageEvent
	json.NewDecoder(listW.Body).Decode(&events)
	if len(events) != 5 {
		t.Errorf("expected 5 events, got %d", len(events))
	}

	statsReq := httptest.NewRequest("GET", "/events/stats", nil)
	statsW := httptest.NewRecorder()
	svc.HandleStats(statsW, statsReq)
	var stats EventStats
	json.NewDecoder(statsW.Body).Decode(&stats)
	if stats.TotalReceived != 5 {
		t.Errorf("expected totalReceived=5, got %d", stats.TotalReceived)
	}

	clearReq := httptest.NewRequest("DELETE", "/events", nil)
	clearW := httptest.NewRecorder()
	svc.HandleClearEvents(clearW, clearReq)
	if clearW.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", clearW.Code)
	}

	listReq2 := httptest.NewRequest("GET", "/events", nil)
	listW2 := httptest.NewRecorder()
	svc.HandleListEvents(listW2, listReq2)
	var events2 []*eventsv1.UsageEvent
	json.NewDecoder(listW2.Body).Decode(&events2)
	if len(events2) != 0 {
		t.Errorf("expected 0 events after clear, got %d", len(events2))
	}
}
