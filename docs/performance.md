# Performance And Resource Budget

ActionRelay must use very little GitHub Actions capacity. The main strategy is a
one-second client-side batch loop: send nothing when idle, and send at most one
package per second when requests are pending.

## Priorities

1. Minimize GitHub API calls, workflow runs, and result-file churn.
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

Stable desktop defaults:

- Batch interval: 1 second.
- Request body: 64 KiB per request.
- Response body: 64 KiB per request.
- Request timeout: 8 seconds.
- Worker fetch concurrency: 4 (bounded by 8).
- Max batch requests: 32.
- Max batch bytes: 256 KiB.
- Max queue requests: 256.
- Cache TTL: 10 seconds.
- Cache max entries: 256.
- Backpressure cooldown: 15 seconds.
- Reliability mode: `fail_closed` by default.
- Stale-if-error fallback: disabled by default (`0`).

## Stable Guardrails

Runtime configuration is validated with bounded production ranges to keep
behavior predictable:

- `batch_interval_ms`: `250..5000`
- `request_timeout_ms`: `1000..30000`
- `max_request_body_bytes`: `1024..1048576`
- `max_response_bytes`: `1024..1048576`
- `max_batch_requests`: `1..64`
- `max_batch_bytes`: `16384..1048576`
- `max_queue_requests`: `32..2048`
- `cache_ttl_ms`: `0..60000`
- `cache_max_entries`: `0..1024`
- `backpressure_cooldown_ms`: `1000..120000`
- `stale_if_error_ttl_ms`: `0..300000`
- `run_start_timeout_sec`: `30..600`
- `run_wait_timeout_sec`: `60..1800`
- `poll_interval_ms`: `500..10000`

## Latency Reality

The batch loop adds up to one second before dispatch. GitHub Actions then adds
queueing, runner startup, result publication, result availability, and polling.
ActionRelay should optimize these costs, but it should not promise real-time VPN
or direct-connection latency.
