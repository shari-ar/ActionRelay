# Performance And Resource Budget

ActionRelay must use very little GitHub Actions capacity. The main strategy is a
one-second client-side batch loop: send nothing when idle, and send at most one
package per second when requests are pending.

## Priorities

1. Minimize GitHub API calls, workflow runs, and artifacts.
2. Return local responses as quickly as the batch model allows.
3. Keep batches and result packages small.
4. Keep worker runtime short.
5. Fail unsupported traffic quickly.

## Batch Strategy

The local route agent should:

- Queue pending requests for the next one-second tick.
- Send a batch only when the queue is non-empty.
- Cap request count and total bytes per batch.
- Deduplicate safe identical requests before dispatch.
- Cache safe responses locally.
- Limit outstanding batches.
- Apply backpressure when GitHub Actions is delayed.

## Worker Strategy

The worker should:

- Process multiple requests from one batch.
- Use strict per-request timeouts.
- Limit internal fetch concurrency.
- Stop work when the batch deadline is reached.
- Return one result package with partial errors if needed.

## Default Budgets

Initial defaults should be conservative:

- Batch interval: 1 second.
- Request body: 64 KiB per request.
- Response body: 64 KiB per request.
- Request timeout: 8 seconds.
- Worker fetch concurrency: low single digits.
- Outstanding batches: very low by default.
- Artifact retention: shortest practical value.

## Latency Reality

The batch loop adds up to one second before dispatch. GitHub Actions then adds
queueing, runner startup, artifact upload, artifact availability, and polling.
ActionRelay should optimize these costs, but it should not promise real-time VPN
or direct-connection latency.
