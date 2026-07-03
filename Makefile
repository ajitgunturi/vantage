# vantage — elastic GPU telemetry pipeline
# Single Go module. cmd/{mq,streamer,collector,gateway} are independent service
# entrypoints; shared code lives in pkg/{pb,db,models}.
#
# Machine-local settings (Docker socket, testcontainers) live in .env
# (gitignored). On first run, make writes a .env template with empty values —
# edit it and set DOCKER_HOST for your Docker provider:
#
#   Rancher Desktop: DOCKER_HOST=unix://$HOME/.rd/docker.sock
#   Docker Desktop:  DOCKER_HOST=unix://$HOME/.docker/run/docker.sock
#
# Docker-dependent targets (test, coverage, e2e, docker, deploy, soak, ...)
# validate the values via the check-env prerequisite. Pure-Go targets
# (build, lint, proto, swagger) never need Docker or .env values.

SERVICES := mq streamer collector gateway
DOCKER_IMAGES := $(SERVICES) migrate
COVERAGE_THRESHOLD ?= 90
PROTO_DIR := api/proto
PB_OUT    := pkg/pb

# Tooling PATH: kind lives in ~/go/bin (not on shell PATH).
export PATH := $(HOME)/go/bin:$(PATH)

# ── Machine-local env (.env, gitignored) ─────────────────────────────────────
# Self-templating: the template is written at Makefile *parse* time (any
# target, including help, triggers creation) so the prompt surfaces
# immediately. Empty values only hard-fail via check-env on targets that
# actually need Docker.
ifeq ($(wildcard .env),)
$(shell printf 'DOCKER_HOST=\nTESTCONTAINERS_RYUK_DISABLED=true\n' > .env)
$(warning Created .env template — set DOCKER_HOST (e.g. unix://$$HOME/.rd/docker.sock))
endif
include .env
export DOCKER_HOST TESTCONTAINERS_RYUK_DISABLED

.DEFAULT_GOAL := help

.PHONY: help tools check-env check-protoc proto build test coverage e2e swagger lint tidy clean \
        smoke smoke-% docker docker-% docker-streamer kind-up helm-install kind-down \
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

check-env: ## Verify machine-local .env values are set (prerequisite of Docker targets)
	@[ -n "$(DOCKER_HOST)" ] || { \
		echo "ERROR: DOCKER_HOST is empty — edit .env"; \
		echo "  Rancher Desktop: unix://$$HOME/.rd/docker.sock"; \
		echo "  Docker Desktop:  unix://$$HOME/.docker/run/docker.sock"; \
		exit 1; }

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

test: check-env ## Run all unit + integration tests with race detector and coverage
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

coverage: check-env ## Enforce >= $(COVERAGE_THRESHOLD)% line coverage on internal/ and pkg/ packages (generated pkg/pb excluded)
	PKGS=$$(go list ./internal/... ./pkg/... | grep -v '/pkg/pb\|/pkg/docs'); \
	go test -race -covermode=atomic -coverprofile=coverage.out -tags=integration $$PKGS
	@go tool cover -func=coverage.out | tail -1
	@total=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "total coverage: $$total% (min $(COVERAGE_THRESHOLD)%)"; \
	awk "BEGIN{exit !($$total >= $(COVERAGE_THRESHOLD))}" || \
		{ echo "FAIL: coverage $$total% < $(COVERAGE_THRESHOLD)%"; exit 1; }

e2e: check-env ## Run end-to-end pipeline tests (requires Docker — see .env, top-of-file comment)
	go test -race -tags=integration -count=1 -v ./test/e2e/...

smoke: ## Run every phase's manual smoke check (all phases shipped so far)
	@found=0; for f in scripts/smoke/phase*.sh; do \
		[ -e "$$f" ] || continue; found=1; echo "== $$f =="; bash "$$f" || exit 1; done; \
	[ "$$found" = 1 ] || echo "no smoke scripts yet under scripts/smoke/"

smoke-%: ## Run one phase's manual smoke check, e.g. make smoke-01
	@found=0; for f in scripts/smoke/phase$*-*.sh; do \
		[ -e "$$f" ] || continue; found=1; echo "== $$f =="; bash "$$f" || exit 1; done; \
	[ "$$found" = 1 ] || { echo "no smoke scripts for phase $* (looked for scripts/smoke/phase$*-*.sh)"; exit 1; }

dev-up: check-env ## Start local dev dependencies (Postgres via docker compose)
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

docker: check-env $(addprefix docker-,$(DOCKER_IMAGES)) ## Build all five images (4 services + migrate)

docker-%: check-env ## Build a single service image (build/%.Dockerfile)
	docker build -f build/$*.Dockerfile -t vantage/$*:dev .

# Explicit rule (overrides docker-%): the streamer image optionally bakes a CSV.
# DEPLOY_CSV must be a build-context-relative path (make deploy stages it under
# build/.deploy/). Without DEPLOY_CSV the image is CSV-less — dev/test flows
# mount testdata/fixture.csv at runtime (see docker-compose.full.yml).
docker-streamer: check-env ## Build the streamer image (DEPLOY_CSV=<context path> bakes a CSV; unset = CSV-less)
	docker build -f build/streamer.Dockerfile \
		$(if $(DEPLOY_CSV),--build-arg DEPLOY_CSV=$(DEPLOY_CSV)) \
		-t vantage/streamer:dev .

kind-up: check-env ## Create local kind cluster
	kind create cluster --name vantage

helm-install: dependency-update ## Install/upgrade the umbrella chart into kind
	# --timeout 6m > the migrate hook's activeDeadlineSeconds (300s): a stuck
	# migration surfaces as the Job's DeadlineExceeded, not Helm's own timeout.
	# Cold first installs pull busybox + bitnami/postgresql from Docker Hub.
	helm upgrade --install vantage deployments -f deployments/values.yaml --timeout 6m

kind-down: ## Delete the kind cluster
	kind delete cluster --name vantage

dependency-update: ## Pull Helm chart dependencies (Bitnami postgresql OCI)
	helm dependency update deployments/

kind-load: check-env ## Load all five vantage/*:dev images into the vantage kind cluster
	@for img in $(DOCKER_IMAGES); do \
		echo "== kind load $$img =="; \
		kind load docker-image vantage/$$img:dev --name vantage; \
	done

# deploy requires an explicit telemetry CSV: pass CSV=<path>, or answer the
# prompt on an interactive terminal. Scripted/CI runs without CSV= fail loudly —
# the streamer image never bakes demo data implicitly. The file is staged into
# the build context (build/.deploy/, gitignored) so any on-disk path works.
deploy: check-env ## Full deploy (CSV=<path> required; prompts on a TTY): docker build -> kind-load -> helm install
	@csv='$(CSV)'; \
	if [ -z "$$csv" ] && [ -t 0 ]; then \
		printf "Path to DCGM telemetry CSV to bake into the streamer image: "; \
		read -r csv; \
	fi; \
	if [ -z "$$csv" ]; then \
		echo "ERROR: no telemetry CSV given — the streamer image bakes no data unless you supply one." >&2; \
		echo "Usage: make deploy CSV=/path/to/your.csv" >&2; \
		exit 1; \
	fi; \
	if [ ! -f "$$csv" ] || [ ! -r "$$csv" ]; then \
		echo "ERROR: CSV not found or not readable: $$csv" >&2; \
		exit 1; \
	fi; \
	echo "== deploy: baking $$csv into the streamer image =="; \
	mkdir -p build/.deploy && cp "$$csv" build/.deploy/dcgm_metrics.csv; \
	$(MAKE) docker DEPLOY_CSV=build/.deploy/dcgm_metrics.csv && \
		$(MAKE) kind-load && $(MAKE) helm-install; \
	status=$$?; rm -rf build/.deploy; exit $$status

soak: check-env ## Run sustained pipeline soak (SOAK_DURATION=60, SOAK_STREAMERS=3)
	@bash scripts/soak.sh

test-harness: check-env ## Run live-infrastructure E2E harness (requires Docker)
	go test -race -tags=e2e -count=1 -v -timeout 120s ./test/harness/...
