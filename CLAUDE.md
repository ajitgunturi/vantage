# vantage — project conventions

Elastic GPU telemetry pipeline + custom message queue. Go multi-module monorepo.
Module path root: `github.com/ajitgunturi/vantage`. Full spec in `PROJECT.md`; live status in `STATE.md`.

## Module map
- `mq/` — custom message queue: `broker/` (durable segment log), `client/` (producer/consumer lib),
  `proto/` + `gen/` (gRPC contract + generated stubs), `cmd/` (broker binary).
- `streamer/` — producer: loops the DCGM CSV, re-stamps timestamps, produces over the MQ.
- `collector/` — consumer: consumes from MQ, parses, upserts into PostgreSQL (idempotent).
- `apigateway/` — REST API + auto-generated OpenAPI.
- `k8s-infra/` — Helm umbrella chart + `kind/` config.
- `docs/adr/` — architecture decisions only (ADR bar: load-bearing forks, not tooling/naming).

Multi-module via `go.work`. Run `go` commands from the module dir, or use Makefile targets.

## Build / test / verify (use the Makefile, don't hand-roll)
- `make build` — build all binaries.
- `make test` — unit tests. `make cover` / `make cover-check` — coverage (gate: 90% line, 100% branch on logic).
- `make lint` — golangci-lint. `make proto` — regenerate gRPC stubs from `mq/proto`.
- `make hooks` — install the pre-commit hook (run once per clone). `make kind` / `make helm` — local k8s.
- After changing Go code, run the relevant module's tests before declaring done; fix failures autonomously.

## Conventions
- Idiomatic Go: `slog` for logging, `testify` for tests, `pgx` for Postgres, Prometheus client for metrics.
- TDD for business logic (MQ segment log, collector upserts, API handlers) — test first, then implement.
- Collector writes must be idempotent: upsert on `(uuid, metric_name, ts)`.
- Canonical GPU identity = `uuid` (ADR-0005, still *Proposed* — confirm before freezing DB schema).
- Conventional Commits; no `Co-Authored-By` trailer. Ephemeral `feat/*|fix/*|chore/*` branch → PR to `main` → merge green.
