# Development

ActionRelay should stay small and explicit. The local route agent must reduce
work before spending GitHub Actions capacity.

## Planned Layout

```text
client/                 Go route agent, batching, and CLI controls
worker/                 Node.js 20 batch worker
schemas/                JSON schemas
protocol/               Protocol examples and compatibility notes
.github/workflows/      Workflow definitions
tests/                  Unit, integration, and route-flow tests
docs/                   Documentation
scripts/                Developer helpers
releases/               Release metadata
```

## Implementation Rules

- Keep whole-device capture and batching in the local Go agent.
- Send at most one batch per second when work is pending.
- Keep outbound internet fetches in the worker.
- Validate batches in both client and worker.
- Treat GitHub APIs as eventually consistent.
- Prefer clear local errors over hanging requests.
- Do not silently support unsupported raw traffic.

## Test Strategy

Recommended tests:

- Route policy tests for supported and unsupported traffic.
- Batch queue tests for timing, count limits, and byte limits.
- Worker validation tests for URL, method, header, and body limits.
- Protocol schema tests.
- Result package parsing tests.
- Negative tests for oversized batches, timeouts, and missing results.
