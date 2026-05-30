# ActionRelay Documentation

ActionRelay routes eligible whole-device requests through a local Go agent. The
agent batches pending requests once per second, sends the batch to GitHub Actions,
and receives one compact result package back.

## Documents

- `architecture.md`: Whole-device route agent, server worker, and batch flow.
- `protocol.md`: JSON envelopes for request batches and result packages.
- `usage.md`: Route setup, operation, and status commands.
- `operations.md`: Day-to-day operator workflow and lifecycle commands.
- `compatibility.md`: Supported platforms, traffic model, and boundaries.
- `limitations.md`: Explicit supported limits and current non-goals.
- `recovery.md`: Deterministic recovery runbooks for common failures.
- `performance.md`: One-second batching, resource budgets, and latency limits.
- `security.md`: Local routing, sensitive traffic, and guardrails.
- `development.md`: Project structure, tests, and implementation rules.
- `release.md`: Build, CI, checksum, and release automation process.
- `final-validation.md`: Required pre-`v1.0` desktop validation checklist.
- `roadmap.md`: Phased delivery plan.

## Reading Order

1. `../README.md`
2. `architecture.md`
3. `performance.md`
4. `protocol.md`
5. `security.md`
6. `usage.md`
7. `operations.md`
8. `compatibility.md`
9. `limitations.md`
10. `recovery.md`
11. `development.md`
12. `release.md`
13. `final-validation.md`
14. `roadmap.md`
