# ActionRelay Documentation

ActionRelay accepts eligible HTTP(S) proxy requests through a local Go agent.
The agent batches pending requests once per second, sends the batch to GitHub
Actions, and receives one compact result package back.

## Reading Order

1. `../README.md`
2. `architecture.md`: Local proxy agent, server worker, and batch flow.
3. `performance.md`: One-second batching, resource budgets, and latency limits.
4. `protocol.md`: JSON envelopes for request batches and result packages.
5. `security.md`: Local routing, sensitive traffic, and guardrails.
6. `usage.md`: Route setup, operation, and status commands.
7. `operations.md`: Day-to-day operator workflow and lifecycle commands.
8. `compatibility.md`: Supported platforms, traffic model, and boundaries.
9. `supported-boundary.md`: Definitive stable product boundary and non-goals.
10. `limitations.md`: Explicit supported limits and current non-goals.
11. `recovery.md`: Deterministic recovery runbooks for common failures.
12. `development.md`: Project structure, tests, and implementation rules.
13. `release.md`: Build, CI, checksum, and release automation process.
14. `final-validation.md`: Required pre-`v1.0` desktop validation checklist.
15. `roadmap.md`: Phased delivery plan.
