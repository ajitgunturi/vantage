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

# Bake the telemetry CSV into the image at /data/dcgm_metrics.csv.
# Prefer the real DCGM export (dcgm_metrics_*.csv — gitignored, local-only,
# present when building on the dev machine); fall back to the committed
# 12-row fixture so builds succeed on clones without the data file.
# The image is built locally and loaded into kind — never pushed to a registry,
# so baking local data does not leak it beyond the dev machine.
RUN mkdir -p /out/data && \
    f=$(ls dcgm_metrics_*.csv 2>/dev/null | head -n1); \
    if [ -n "$f" ]; then \
        echo "baking real DCGM CSV: $f"; cp "$f" /out/data/dcgm_metrics.csv; \
    else \
        echo "no dcgm_metrics_*.csv in context — baking committed fixture"; \
        cp build/fixture/dcgm_metrics.csv /out/data/dcgm_metrics.csv; \
    fi

# ── Stage 2: final ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS final
COPY --from=builder /out/streamer /streamer
COPY --from=builder /out/data /data
ENTRYPOINT ["/streamer"]
