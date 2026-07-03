# build/streamer.Dockerfile — Streamer — gRPC Produce client; no listen port (client-only)
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
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/streamer ./cmd/streamer

# Bake the telemetry CSV into the image at /data/dcgm_metrics.csv — ONLY when
# DEPLOY_CSV is passed (a context-relative path, staged by `make deploy CSV=...`).
# Without it the image is CSV-less: dev/test flows (docker compose, e2e/smoke)
# mount testdata/fixture.csv over /data/dcgm_metrics.csv at runtime instead.
# There is deliberately NO fallback — a deploy must choose its data explicitly.
# The image is built locally and loaded into kind — never pushed to a registry,
# so baking local data does not leak it beyond the dev machine.
ARG DEPLOY_CSV=
RUN mkdir -p /out/data && \
    if [ -n "$DEPLOY_CSV" ]; then \
        echo "baking DCGM CSV: $DEPLOY_CSV"; cp "$DEPLOY_CSV" /out/data/dcgm_metrics.csv; \
    else \
        echo "no DEPLOY_CSV — CSV-less image (runtime mount required, or build via 'make deploy CSV=...')"; \
    fi

# ── Stage 2: final ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS final
COPY --from=builder /out/streamer /streamer
COPY --from=builder /out/data /data
ENTRYPOINT ["/streamer"]
