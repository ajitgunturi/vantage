# ADR-0007: Test-driven development with enforced quality gates

- Status: Accepted
- Date: 2026-06-24

## Context
The pipeline is systems-level code where correctness (durability, ordering, idempotency) is easy to
get subtly wrong. We want defects caught early and coverage held high, with fast local feedback so
problems never reach `main`.

## Decision
Adopt **test-driven development** for every component, backed by layered enforcement:

**Coverage gates (CI, required via `ci-success`):**
- **≥90% line coverage** per module — native `go test -coverprofile` + `scripts/coverage-gate.sh`.
- **100% branch/logic coverage** on `internal/` logic packages — `gobco` (condition coverage) via
  `scripts/logic-coverage.sh`. Go has no native branch coverage, so gobco supplies it.
- **Scope:** coverage measures logic packages only. Generated stubs (`/gen/`) and thin `main` wiring
  (`/cmd/`) are excluded — you don't unit-test codegen or flag-parsing glue.
- **Fail-open until code exists:** a module with no testable packages is skipped, not failed, so the
  gate is green at scaffold time and bites the moment TDD adds the first covered line.

**Lint + format (local pre-commit AND CI):**
- A `.githooks/pre-commit` hook (installed by `make hooks` via `core.hooksPath`) runs `gofmt` + 
  `golangci-lint` on staged Go files before every commit — fast feedback, blocks ill-formatted code.
- The **same** checks run as a CI `lint` job, because hooks are bypassable (`--no-verify`); CI is the
  real gate.

## Driving Prompts
> another dimension - we must have 90% lines of code coverage and 100% logic coverage - let us
> implement test driven development approach for the components we are going to build here.

> we need a pre commit hook - ill formatted code - lint related issues should be caught before we
> push any code to remote.

## Consequences
- (+) Correctness pinned by tests written first; regressions caught at the PR boundary.
- (+) Fast local signal (hook) + authoritative enforcement (CI) — defense in depth.
- (−) 100% branch coverage is demanding; mitigated by scoping it to `internal/` logic only.
- (−) gobco's output parsing is heuristic ("was never" → uncovered); validate against the first real
  logic package and tighten if needed.
- (−) golangci-lint pinned to `@latest` (v2) for now; pin a version if schema drift bites.

## Alternatives considered
- **Statement coverage only (no gobco)** — simpler, but doesn't prove every branch is exercised; the
  user explicitly asked for logic coverage.
- **Pre-commit hook only (no CI lint)** — rejected: bypassable, so not a real gate.
- **Test-after** — rejected: the user chose TDD.
