# vantage — elastic GPU telemetry pipeline
# Single Go module. cmd/{mq,streamer,collector,gateway} are independent service
# entrypoints; shared code lives in pkg/{pb,db,models}.
#
# Integration tests (coverage, e2e) require Docker / Rancher Desktop.
# Set these environment variables before running them:
#
#   export DOCKER_HOST=unix://$$HOME/.rd/docker.sock
#   export TESTCONTAINERS_RYUK_DISABLED=true
#
# Or prefix make targets directly:
#
#   DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true make coverage
#   DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true make e2e

SERVICES := mq streamer collector gateway
DOCKER_IMAGES := $(SERVICES) migrate
COVERAGE_THRESHOLD ?= 90
PROTO_DIR := api/proto
PB_OUT    := pkg/pb

# Tooling env for every target: kind lives in ~/go/bin (not on shell PATH),
# Docker/kind/testcontainers talk to the Rancher Desktop socket, Ryuk stays off.
export PATH := $(HOME)/go/bin:$(PATH)
export DOCKER_HOST := unix:///Users/ajitg/.rd/docker.sock
export TESTCONTAINERS_RYUK_DISABLED := true

.DEFAULT_GOAL := help

.PHONY: help tools check-protoc proto build test coverage e2e swagger lint tidy clean \
        smoke smoke-% docker docker-% kind-up helm-install kind-down \
        dev-up dev-down kind-load deploy dependency-update soak test-harness

help: ## List targets
	@grep -hE '^[a-zA-Z_%-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

tools: ## Install dev tools (protoc plugins, swag, golangci-lint, kind)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/swaggo/swag/cmd/swag@v1.16.4
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install sigs.k8s.io/kind@latest

check-protoc: ## Verify protoc binary is present (install: brew install protobuf / apt install protobuf-compiler)
	@command -v protoc >/dev/null 2>&1 || { \
		echo "ERROR: protoc not found. Install it first:"; \
		echo "  macOS:          brew install protobuf"; \
		echo "  Debian/Ubuntu:  apt install protobuf-compiler"; \
		exit 1; \
	}
	@protoc --version

proto: check-protoc ## Compile .proto schemas into pkg/pb
	@mkdir -p $(PB_OUT)
	protoc -I $(PROTO_DIR) \
		--go_out=$(PB_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PB_OUT) --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/*.proto

build: ## Build all service binaries that exist (services land phase by phase)
	@for s in $(SERVICES); do \
		if [ -d ./cmd/$$s ]; then echo "== build $$s =="; go build -o bin/$$s ./cmd/$$s; \
		else echo "-- skip $$s (cmd/$$s not present yet) --"; fi; done

test: ## Run all unit + integration tests with race detector and coverage
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

coverage: ## Enforce >= $(COVERAGE_THRESHOLD)% line coverage on internal/ and pkg/ packages (generated pkg/pb excluded)
	PKGS=$$(go list ./internal/... ./pkg/... | grep -v '/pkg/pb\|/pkg/docs'); \
	go test -race -covermode=atomic -coverprofile=coverage.out -tags=integration $$PKGS
	@go tool cover -func=coverage.out | tail -1
	@total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "total coverage: $$total% (min $(COVERAGE_THRESHOLD)%)"; \
	awk "BEGIN{exit !($$total >= $(COVERAGE_THRESHOLD))}" || \
		{ echo "FAIL: coverage $$total% < $(COVERAGE_THRESHOLD)%"; exit 1; }

e2e: ## Run end-to-end pipeline tests (requires Docker / Rancher Desktop — see top-of-file env vars)
	go test -race -tags=integration -count=1 -v ./test/e2e/...

smoke: ## Run every phase's manual smoke check (all phases shipped so far)
	@found=0; for f in scripts/smoke/phase*.sh; do \
		[ -e "$$f" ] || continue; found=1; echo "== $$f =="; bash "$$f" || exit 1; done; \
	[ "$$found" = 1 ] || echo "no smoke scripts yet under scripts/smoke/"

smoke-%: ## Run one phase's manual smoke check, e.g. make smoke-01
	@found=0; for f in scripts/smoke/phase$*-*.sh; do \
		[ -e "$$f" ] || continue; found=1; echo "== $$f =="; bash "$$f" || exit 1; done; \
	[ "$$found" = 1 ] || { echo "no smoke scripts for phase $* (looked for scripts/smoke/phase$*-*.sh)"; exit 1; }

dev-up: ## Start local dev dependencies (Postgres via docker compose)
	docker compose up -d --wait

dev-down: ## Stop local dev dependencies
	docker compose down

swagger: ## Auto-generate the OpenAPI spec from gateway code annotations
	swag init -g cmd/gateway/main.go -o pkg/docs

lint: ## Lint (golangci-lint when installed, fallback go vet — G-2: errors are never swallowed)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found — running go vet (install via: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)"; \
		go vet ./...; \
	fi

tidy: ## go mod tidy
	go mod tidy

clean: ## Remove build + coverage artifacts
	rm -rf bin coverage.out coverage.html

docker: $(addprefix docker-,$(DOCKER_IMAGES)) ## Build all five images (4 services + migrate)

docker-%: ## Build a single service image (build/%.Dockerfile)
	docker build -f build/$*.Dockerfile -t vantage/$*:dev .

kind-up: ## Create local kind cluster
	kind create cluster --name vantage

helm-install: dependency-update ## Install/upgrade the umbrella chart into kind
	helm upgrade --install vantage deployments -f deployments/values.yaml --timeout 3m

kind-down: ## Delete the kind cluster
	kind delete cluster --name vantage

dependency-update: ## Pull Helm chart dependencies (Bitnami postgresql OCI)
	helm dependency update deployments/

kind-load: ## Load all five vantage/*:dev images into the vantage kind cluster
	@for img in $(DOCKER_IMAGES); do \
		echo "== kind load $$img =="; \
		kind load docker-image vantage/$$img:dev --name vantage; \
	done

deploy: docker kind-load helm-install ## Full deploy cycle: docker build -> kind-load -> helm install

soak: ## Run sustained pipeline soak (SOAK_DURATION=60, SOAK_STREAMERS=3)
	@bash scripts/soak.sh

test-harness: ## Run live-infrastructure E2E harness (requires Docker)
	DOCKER_HOST=unix://$$HOME/.rd/docker.sock TESTCONTAINERS_RYUK_DISABLED=true \
	  go test -race -tags=e2e -count=1 -v -timeout 120s ./test/harness/...
