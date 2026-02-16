# external-events-template

Template / demo implementations of the [EdgeQuota](https://github.com/edgequota/edgequota) external events protocol.

EdgeQuota emits batched usage events (rate-limit decisions) to an external service when the events feature is enabled. This is a fire-and-forget webhook pattern -- events are buffered and flushed asynchronously, never blocking the request hot path. This repository provides two ready-to-use implementations -- one using **gRPC** and one using plain **HTTP** -- that demonstrate how to:

1. Receive batched usage events from EdgeQuota.
2. Store events in memory with aggregate counters.
3. Expose a query API to inspect events and statistics.

## Project structure

```
external-events-template/
├── grpc/              # gRPC implementation (edgequota.events.v1.EventService)
│   ├── main.go        # gRPC server (:50053) + HTTP query API (:8083)
│   ├── events.go      # EventService implementation
│   ├── events_test.go
│   ├── gen/           # Generated Go stubs (buf generate)
│   ├── buf.gen.yaml
│   ├── Dockerfile
│   └── go.mod
├── http/              # HTTP implementation (JSON POST /events)
│   ├── main.go        # HTTP server (:8080) with /events endpoints
│   ├── events.go      # Event handler implementation
│   ├── events_test.go
│   ├── Dockerfile
│   └── go.mod
├── Makefile
└── README.md
```

## Protocol overview

### gRPC

EdgeQuota calls `edgequota.events.v1.EventService/PublishEvents` with a `PublishEventsRequest` containing a batch of `UsageEvent` messages. The service returns a `PublishEventsResponse` with the number of accepted events.

Proto definitions: [buf.build/edgequota/edgequota](https://buf.build/edgequota/edgequota)

### HTTP

EdgeQuota sends a `POST` to the configured URL with a JSON body matching the `PublishEventsRequest` schema.

**Request body** (JSON):
```json
{
  "events": [
    {
      "key": "10.0.0.1",
      "tenant_key": "tenant-pro",
      "method": "GET",
      "path": "/api/v1/resource",
      "allowed": true,
      "remaining": 95,
      "limit": 100,
      "timestamp": "2026-02-16T21:00:00Z",
      "status_code": 200,
      "request_id": "abc123"
    },
    {
      "key": "10.0.0.1",
      "tenant_key": "tenant-pro",
      "method": "POST",
      "path": "/api/v1/resource",
      "allowed": false,
      "remaining": 0,
      "limit": 100,
      "timestamp": "2026-02-16T21:00:01Z",
      "status_code": 429,
      "request_id": "def456"
    }
  ]
}
```

**Response body** (JSON):
```json
{
  "accepted": 2
}
```

### UsageEvent fields

| Field | Type | Description |
|---|---|---|
| `key` | string | Rate limit bucket key |
| `tenant_key` | string | Tenant key (if assigned by external RL service) |
| `method` | string | HTTP method |
| `path` | string | Request path |
| `allowed` | bool | Whether the request was allowed |
| `remaining` | int64 | Remaining tokens after this decision |
| `limit` | int64 | Configured limit (burst) |
| `timestamp` | string | RFC 3339 timestamp |
| `status_code` | int32 | HTTP status code returned |
| `request_id` | string | X-Request-Id for deduplication and correlation |

## Quick start

### gRPC variant

```bash
cd grpc
go run .

# Test with grpcurl
grpcurl -plaintext -d '{
  "events": [{
    "key": "10.0.0.1",
    "tenant_key": "tenant-1",
    "method": "GET",
    "path": "/api/v1/test",
    "allowed": true,
    "remaining": 99,
    "limit": 100,
    "timestamp": "2026-02-16T21:00:00Z",
    "status_code": 200,
    "request_id": "req-1"
  }]
}' localhost:50053 edgequota.events.v1.EventService/PublishEvents

# Query events
curl http://localhost:8083/events
curl http://localhost:8083/events?tenant_key=tenant-1&limit=10
curl http://localhost:8083/events/stats

# Clear events
curl -X DELETE http://localhost:8083/events
```

### HTTP variant

```bash
cd http
go run .

# Publish events
curl -X POST http://localhost:8080/events \
  -H 'Content-Type: application/json' \
  -d '{
    "events": [{
      "key": "10.0.0.1",
      "tenant_key": "tenant-1",
      "method": "GET",
      "path": "/api/v1/test",
      "allowed": true,
      "remaining": 99,
      "limit": 100,
      "timestamp": "2026-02-16T21:00:00Z",
      "status_code": 200,
      "request_id": "req-1"
    }]
  }'

# Query events
curl http://localhost:8080/events
curl "http://localhost:8080/events?tenant_key=tenant-1&limit=10"
curl http://localhost:8080/events/stats

# Clear events
curl -X DELETE http://localhost:8080/events
```

## EdgeQuota configuration

### gRPC events

```yaml
events:
  enabled: true
  batch_size: 100
  flush_interval: "5s"
  buffer_size: 10000
  grpc:
    address: "events-service:50053"
```

### HTTP events

```yaml
events:
  enabled: true
  batch_size: 100
  flush_interval: "5s"
  buffer_size: 10000
  http:
    url: "http://events-service:8080/events"
```

## Query API

Both variants expose HTTP endpoints for inspecting received events:

| Method | Path | Description |
|---|---|---|
| `GET` | `/events` | List stored events (newest first) |
| `GET` | `/events?tenant_key=X` | Filter by tenant key |
| `GET` | `/events?limit=N` | Limit results (default: 100) |
| `GET` | `/events/stats` | Aggregate counters (received, allowed, denied) |
| `DELETE` | `/events` | Clear all stored events and reset counters |

## Configuration

| Flag / Env var | Default | Description |
|---|---|---|
| `-grpc-addr` / `GRPC_ADDR` | `:50053` | gRPC listen address (gRPC variant only) |
| `-http-addr` / `HTTP_ADDR` | `:8083` | HTTP listen address for query API (gRPC variant) |
| `-addr` / `ADDR` | `:8080` | HTTP listen address (HTTP variant) |

## Docker

```bash
# gRPC
docker build -t edgequota-events-grpc grpc/
docker run -p 50053:50053 -p 8083:8083 edgequota-events-grpc

# HTTP
docker build -t edgequota-events-http http/
docker run -p 8080:8080 edgequota-events-http
```

## Tests

```bash
make test
```

## Regenerating gRPC stubs

```bash
cd grpc && buf generate
```

Proto definitions are pulled from the [Buf Schema Registry](https://buf.build/edgequota/edgequota).

## Extending this template

To add your own event processing logic:

1. **gRPC**: Modify `EventService.PublishEvents()` in `grpc/events.go`.
2. **HTTP**: Modify `EventService.HandlePublishEvents()` in `http/events.go`.

Key extension points:
- Persist events to a database (PostgreSQL, ClickHouse, BigQuery, etc.).
- Forward events to a message queue (Kafka, NATS, SQS).
- Compute real-time analytics and dashboards.
- Implement billing based on usage events.
- Use `request_id` for idempotent event processing.
- Filter and aggregate events by `tenant_key` for multi-tenant billing.

## License

MIT
