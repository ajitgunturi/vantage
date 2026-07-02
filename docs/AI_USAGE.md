# AI-Assisted Development

This project was built with the assistance of Claude (Anthropic) as a primary
implementation and code-review partner, operating under the
[GSD (Get Stuff Done) framework](https://github.com/opengsd/gsd-core).

---

## What AI was used for

| Activity | AI role |
|---|---|
| Architecture decisions | Research and trade-off synthesis (human made final calls) |
| ADR authoring | Drafted by AI, reviewed and accepted by human |
| Code generation | Primary implementation across all four services |
| Test design | TDD-first: AI wrote RED tests, then GREEN implementations |
| Code review | AI-driven mid-assignment review (see `.planning/` phase findings) |
| Bug fixing | All CRITICAL/MAJOR findings identified and fixed by AI |
| Documentation | README, OpenAPI annotations, ADRs |

---

## Human oversight

Every commit, architectural decision, and protocol contract was reviewed by the
human author before merging. AI-generated code was never merged without human
inspection of:

- Protocol buffer contracts (`api/proto/mq.proto`)
- Database schema and migration files (`pkg/db/migrations/`)
- Security-sensitive paths (authentication, SQL parameterisation, input validation)
- Concurrency-critical code (MQ ring buffer, bidi stream send/recv split)

---

## Model and tooling

- **Model:** Claude Sonnet (Anthropic)
- **Orchestration:** GSD framework (`/gsd-execute-phase`, `/gsd-plan-phase`)
- **Commit discipline:** All commits use Conventional Commits; no AI authorship
  trailers are included in the git history

---

## Known limitations introduced by AI assistance

- AI-generated comments occasionally exceed necessary verbosity; tighten on
  review.
- Test coverage metrics were targeted (≥90%) rather than emergent; some edge
  cases that a human would have found by pure intuition may be absent.
- The mid-assignment review (phase quick-260702-ku8) found several correctness
  and liveness gaps that were not caught in the initial implementation passes;
  these are documented in `.planning/quick/260702-ku8-*/`.
