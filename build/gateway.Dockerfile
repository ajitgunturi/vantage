# build/gateway.Dockerfile — API Gateway — HTTP REST + Swagger UI (8080); pkg/docs compiled in via Go import
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
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gateway ./cmd/gateway

# ── Stage 2: final ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS final
COPY --from=builder /out/gateway /gateway
EXPOSE 8080
ENTRYPOINT ["/gateway"]
