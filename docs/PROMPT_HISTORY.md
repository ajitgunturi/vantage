# Prompt History

A verbatim, chronological log of the user instructions that shaped **vantage**, each with a one-line
note on what was done. It exists for AI-usage transparency: it shows which parts of the work were
human-directed versus AI-executed.

Architecture decisions are distilled in [`adr/`](adr/); this file is the raw prompt trail.

---

**1. Bootstrap context**
> I have a programming assignment ... read that and create a context. We are allowed to use opensource
> database such as postgresql ... We must create a custom message queue solution - deployable as an
> independent installation with a persistence layer integrated so that it can survive down times -
> also we should account for the performance of the system with different combinations - producers >
> consumers, producers < consumers and producers = consumers - with max scale of 10 ... Read the pdf
> and wait for further instructions.

Read the assignment PDF + profiled the DCGM CSV (10 metrics, 31 hosts × 8 GPUs, 247 GPUs); wrote
`PROJECT.md` and `STATE.md`.

**2. Monorepo structure**
> We will build a monorepo structure - have 5 modules - 1 module each for - streamer, collector,
> message queue, api gateway and finally the k8s-infra files ...

Created the directory tree, `go.work`, the MQ gRPC proto contract, and per-module `go.mod`. → ADR-0003.

**3. Structural choices** (answered via options)
> Module layout = 5 separate go.mod (multi-module) · MQ transport = gRPC streaming · Local K8s = kind

Locked in. → ADR-0003 (modules), ADR-0004 (gRPC). kind and buf are tooling picks (no ADR).

**4. Naming**
> we need a good name for this telemetry pipeline ... org name as - ajitgunturi -
> github.com/ajitgunturi/... and the repo name could be vantage

Renamed all module paths to `github.com/ajitgunturi/vantage/...`, regenerated gRPC stubs.

**5. Decision-record discipline**
> docs will capture ADRs and PROMPT_HISTORY.md files. For each major architectural decision ... we
> will record the prompt instruction user has given to claude - ... an artifact for the interview panel.

Established `docs/adr/` + this log, each ADR carrying its Driving Prompt.

> ADRs are too exhaustive - we do not need ADR for every prompt - it has to hold some kind of meaning.

Pruned ADRs to load-bearing decisions only.

> ADRs are too exhaustive ... only preserve the architecture decision records - not the ci models and
> branching strategies, etc - ... also clean up the PROMPT_HISTORY.md file

Scoped ADRs to architecture only (0001–0005); moved branching/CI/testing to operational docs
(`BRANCHING.md`, `Makefile`, CI); cleaned up this file.

**6. Branching & CI**
> vantage will have the main branch protected. We will have 5 independent feature branches ... every
> merge to main will trigger a build and run unit tests - all PRs to have a build action ... -
> independent branches will never have builds ... we do not want to delete these feature branches.

Added `.github/workflows/ci.yml` (PR/push-to-main only), `BRANCHING.md`, and `scripts/setup-github.sh`;
created the repo + protection. (Process, not architecture — documented operationally, not as an ADR.)

**7. Test-driven development + coverage**
> we must have 90% lines of code coverage and 100% logic coverage - let us implement test driven
> development approach for the components we are going to build here.

Added coverage gates — 90% line (native `go test`) + 100% branch (gobco) — into `Makefile` + CI,
scoped to logic packages. Fixed the first red build (placeholder modules reverted to bare `go.mod`).

**8. Pre-commit hook**
> we need a pre commit hook - ill formatted code - lint related issues should be caught before we
> push any code to remote.

Added `.githooks/pre-commit` (gofmt + golangci-lint), `make hooks`, and a matching CI lint job.

**9. Simplify branching (solo dev)**
> clean up the multiple feature branches - I am the only developer working on this repo - I am over
> engineering this whole experience - remove the restrictions put on the feature branches and just
> protect main branch - our previous behavior should still work as expected.

Removed the feature-branch ruleset + the 5 long-lived branches; kept `main` protected and the CI
behaviour. Work now happens on ephemeral branches PR'd to `main`.
