# ActionRelay Documentation

ActionRelay routes eligible whole-device requests through a local Go agent. The
agent batches pending requests once per second, sends the batch to GitHub Actions,
and receives one compact result package back.

## Documents

- `architecture.md`: Whole-device route agent, server worker, and batch flow.
- `protocol.md`: JSON envelopes for request batches and result packages.
- `usage.md`: Route setup, operation, and status commands.
- `performance.md`: One-second batching, resource budgets, and latency limits.
- `security.md`: Local routing, sensitive traffic, and guardrails.
- `development.md`: Project structure, tests, and implementation rules.
- `release.md`: Build, CI, checksum, and release automation process.
- `roadmap.md`: Phased delivery plan.

## Reading Order

1. `../README.md`
2. `architecture.md`
3. `performance.md`
4. `protocol.md`
5. `security.md`
6. `usage.md`
7. `development.md`
8. `release.md`
9. `roadmap.md`
