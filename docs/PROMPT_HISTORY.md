# Prompt History

Chronological, **verbatim** log of the user instructions that shaped this project, paired with what
Claude did in response. This is a deliverable artifact for the interview panel: it shows exactly
which parts of the workflow were human-directed vs. AI-executed, and where AI assistance needed
correction.

Curated decision-level rationale lives in [`adr/`](adr/). This file is the raw trail.

Legend: 🟦 user prompt · 🟩 AI action · ⚠️ where the prompt/AI fell short and needed intervention.

---

## P1 — Bootstrap context from the assignment
🟦 **User:**
> I have a programming assignment - available in the pdf file in the current folder. I want you to
> read that and create a context. We are allowed to use opensource database such as postgresql to
> store the data the collector will collect and store. We must create a custom message queue
> solution - deployable as an independent installation with a persistence layer integrated so that
> it can survive down times - also we should account for the performance of the system with
> different combinations - producers > consumers, producers < consumers and producers = consumers -
> with max scale of 10 as per the assignment file. Read the pdf and wait for further instructions.

🟩 **AI:** Read the PDF (3 pages) + profiled `dcgm_metrics_20250718_134233.csv` (10 DCGM metrics,
31 hosts × 8 GPUs, 247 GPUs). Wrote `PROJECT.md` (full context) and `STATE.md`. Surfaced 7 open
design questions. → no ADR (context capture, not a decision).

---

## P2 — Monorepo with 5 modules
🟦 **User:**
> We will build a monorepo structure - have 5 modules - 1 module each for - streamer, collector,
> message queue, api gateway and finally the k8s-infra files that would be easily deployed to k8s
> type system locally and also have helmchart configs.

🟩 **AI:** Asked 3 structural questions (module layout / MQ transport / local-k8s). Built the
directory tree, `go.work`, `.gitignore`, MQ proto contract, buf config. → drives **ADR-0003**.

---

## P3 — Structural decisions (answered via question prompt)
🟦 **User selected:**
> Module layout = **5 separate go.mod (multi-module)** · MQ transport = **gRPC streaming** ·
> Local K8s = **kind**

🟩 **AI:** Locked these in. → drives **ADR-0003** (modules) + **ADR-0004** (gRPC). kind and buf are
reversible tooling picks (no ADR — recorded here): toolchain probe found `protoc`/`kind`/`buf` absent
but all `go install`-able, so chose **kind** for local k8s and **buf** for hermetic codegen.

---

## P4 — Capture ADRs + prompt provenance
🟦 **User:**
> docs will capture ADRs and PROMPT_HISTORY.md files. For each major architectural decision we come
> up with during the course of this exercise we will record the prompt instruction user has given to
> claude - this will be one artifact we will produce for the interview panel.

🟩 **AI:** Created `docs/adr/` (template + index) and this file. Established the rule: **an ADR is
reserved for load-bearing architectural decisions with a real trade-off; every prompt is logged here
regardless.** Tooling/branding picks stay in this log, not in ADRs.

---

## P5 — Name the project
🟦 **User:**
> we need a good name for this telemetry pipeline - for my GH repo - any suggestions?

🟩 **AI:** Proposed gridpulse / teleforge / fluxgrid / sluice with rationale (flow/telemetry
metaphors, brandability, module-path impact).

🟦 **User:**
> we need the org name as - ajitgunturi - github.com/ajitgunturi/... and the repo name could be vantage

🟩 **AI:** Renamed all module paths to `github.com/ajitgunturi/vantage/...`, moved proto to
`proto/mqv1/`, regenerated gRPC stubs, verified `mq` builds. (Naming is a branding pick, not an ADR —
recorded here only.)

---

## P6 — ADRs were too exhaustive
🟦 **User:**
> ADRs are too exhaustive - we do not need ADR for every prompt - it has to hold some kind of meaning.

🟩 **AI:** Pruned ADRs from 8 → 5. Removed kind (tooling), buf (tooling), vantage-name (branding);
folded them into this log. Renumbered GPU-id ADR 0007 → 0005. Set the bar in `adr/README.md`: an ADR
is only for load-bearing decisions with a genuine road-not-taken.

---

## P7 — Branching & CI model
🟦 **User:**
> vantage will have the main branch protected. We will have 5 independent feature branches. All the
> commits go to main and every merge to main will trigger a build and run unit tests - all PRs to
> have a build action that does the same as well so we do not break anything - independent branches
> will never have builds. All feature branches should be protected too. There will be too many PRs
> that will be merged and we do not want to delete these feature branches.

🟩 **AI:** Added `.github/workflows/ci.yml` (PR+push-to-main only; build-test matrix ×4 modules +
helm-lint + `ci-success` gate), `docs/BRANCHING.md`, and idempotent `scripts/setup-github.sh`
(repo + 5 long-lived `feat/*` branches + Rulesets: main PR/check-gated, feat/* deletion+force-push
protected, auto-delete disabled). → drives **ADR-0006**.

---

<!--
APPEND TEMPLATE (newest at bottom):

## P<N> — <short title>
🟦 **User:**
> <verbatim prompt>

🟩 **AI:** <what was done>. → drives **ADR-XXXX** (or: no ADR — <why>).
-->
