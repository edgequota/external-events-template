.PHONY: all generate build test lint clean docker-grpc docker-http docker run-grpc run-http

all: generate build test

# ── Code generation ──────────────────────────────────────────────────
generate:
	cd grpc && buf generate

# ── Build ────────────────────────────────────────────────────────────
build:
	cd grpc && go build -o bin/events-server .
	cd http && go build -o bin/events-server .

# ── Test ─────────────────────────────────────────────────────────────
test:
	cd grpc && go test ./...
	cd http && go test ./...

test-verbose:
	cd grpc && go test -v ./...
	cd http && go test -v ./...

test-race:
	cd grpc && go test -race ./...
	cd http && go test -race ./...

# ── Lint ─────────────────────────────────────────────────────────────
lint:
	cd grpc && go vet ./...
	cd http && go vet ./...

# ── Tidy ─────────────────────────────────────────────────────────────
tidy:
	cd grpc && go mod tidy
	cd http && go mod tidy

# ── Run ──────────────────────────────────────────────────────────────
run-grpc:
	cd grpc && go run .

run-http:
	cd http && go run .

# ── Docker ───────────────────────────────────────────────────────────
docker: docker-grpc docker-http

docker-grpc:
	docker build -t edgequota-events-grpc grpc/

docker-http:
	docker build -t edgequota-events-http http/

# ── Clean ────────────────────────────────────────────────────────────
clean:
	rm -rf grpc/bin http/bin
