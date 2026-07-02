# build/migrate.Dockerfile — Migrate — one-shot schema migration job (reads VANTAGE_DB_DSN, exits 0 on success)
# Multi-stage: full Go toolchain in builder, distroless static final (D-08).

# ── Stage 1: builder ────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Dependency manifests first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Source tree (all services share the single module root).
COPY . .

# Static binary: CGO_ENABLED=0 — no libc needed in distroless.
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/migrate ./cmd/migrate

# ── Stage 2: final ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS final
COPY --from=builder /out/migrate /migrate
ENTRYPOINT ["/migrate"]
