# vantage — elastic GPU telemetry pipeline
# Multi-module Go monorepo. Most targets fan out over the 4 Go modules.

MODULES := mq streamer collector apigateway
GOBIN   := $(shell go env GOPATH)/bin
COVERAGE_THRESHOLD ?= 90

.DEFAULT_GOAL := help

.PHONY: help tools hooks proto build test cover cover-check cover-logic cover-html \
        openapi tidy lint clean kind-up kind-load helm-install kind-down

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

tools: ## Install dev tools (buf, protoc plugins, gobco, golangci-lint, kind)
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/rillig/gobco@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install sigs.k8s.io/kind@latest

hooks: ## Install git pre-commit hook (gofmt + lint) via core.hooksPath
	git config core.hooksPath .githooks
	@echo "✓ git hooks installed (core.hooksPath=.githooks)"

proto: ## Regenerate gRPC stubs from proto/mqv1
	cd mq && PATH="$(GOBIN):$$PATH" buf generate

build: ## Build every module standalone (GOWORK=off = Docker-parity)
	@for m in $(MODULES); do echo "== build $$m =="; (cd $$m && GOWORK=off go build ./...); done

# Coverage scope excludes generated stubs (/gen/) and thin main wiring (/cmd/);
# real logic lives in internal/ packages, which are what the gates measure.
test: ## Unit-test every module with race + coverage profile
	@for m in $(MODULES); do echo "== test $$m =="; \
		(cd $$m && pkgs="$$(GOWORK=off go list ./... 2>/dev/null | grep -vE '/(gen|cmd)/' || true)"; \
		if [ -n "$$pkgs" ]; then \
			GOWORK=off go test -race -covermode=atomic -coverprofile=coverage.out $$pkgs; \
		else echo "no testable packages yet — skip"; rm -f coverage.out; fi); done

cover: test ## Print per-module coverage totals
	@for m in $(MODULES); do \
		if [ -s $$m/coverage.out ]; then echo "== $$m =="; go tool cover -func=$$m/coverage.out | tail -1; fi; done

cover-check: test ## Enforce >= $(COVERAGE_THRESHOLD)% line coverage
	COVERAGE_THRESHOLD=$(COVERAGE_THRESHOLD) bash scripts/coverage-gate.sh $(addsuffix /coverage.out,$(MODULES))

cover-logic: ## Enforce 100% branch/logic coverage on the MQ core (mq/ logic pkgs) (gobco)
	bash scripts/logic-coverage.sh

cover-html: test ## Write per-module HTML coverage reports
	@for m in $(MODULES); do [ -s $$m/coverage.out ] && go tool cover -html=$$m/coverage.out -o $$m/coverage.html || true; done

openapi: ## Generate OpenAPI spec (wired once apigateway exists)
	@echo "openapi: pending apigateway module (swag init -g cmd/apigateway/main.go)"

tidy: ## go mod tidy across modules
	@for m in $(MODULES); do (cd $$m && go mod tidy); done

lint: ## Lint all modules (golangci-lint, fallback go vet)
	bash scripts/lint.sh

clean: ## Remove coverage artifacts
	@for m in $(MODULES); do rm -f $$m/coverage.out $$m/coverage.html; done

kind-up: ## Create local kind cluster
	kind create cluster --name vantage --config k8s-infra/kind/cluster.yaml

kind-load: ## Load locally built images into kind
	@echo "kind-load: wired once Dockerfiles + image names exist"

helm-install: ## Install the umbrella chart into kind
	helm upgrade --install vantage k8s-infra/helm/telemetry

kind-down: ## Delete the kind cluster
	kind delete cluster --name vantage
