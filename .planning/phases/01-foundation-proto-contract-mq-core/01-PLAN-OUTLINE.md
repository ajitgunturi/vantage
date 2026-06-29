# Phase 01 — Plan Outline

| Plan ID | Objective | Wave | Depends On | Requirements |
|---------|-----------|------|------------|--------------|
| 01-01 | Module scaffold: verify/install protoc binary, write `api/proto/mq.proto` (TelemetryMessage + Produce/Consume RPCs), run `make proto` to generate `pkg/pb`, scope Makefile coverage gate to exclude `pkg/pb` and `cmd/` | 0 | — | MQ-01, MQ-02 |
| 01-02 | `Store` interface + bounded ring-buffer backend (`internal/queue`): drop-oldest on full, thread-safe enqueue/dequeue/inspect, unit tests + race tests (N produced = N consumed across K consumers, buffer ≥2×N, `go test -race -count=50` clean) | 1 | 01-01 | MQ-03, MQ-04, MQ-05, MQ-08, QA-02 |
| 01-03 | gRPC server (`Produce` unary, `Consume` server-stream, keepalive ServerParameters + EnforcementPolicy, dispatch goroutine, lock released before channel send) + HTTP `GET /api/v1/queue/inspect` (stdlib, JSON: depth/capacity/produced/consumed/dropped/consumers) + disconnect/no-goroutine-leak tests | 2 | 01-02 | MQ-01, MQ-02, MQ-06, MQ-07 |
