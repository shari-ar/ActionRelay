# Architecture

ActionRelay is a desktop HTTP(S) proxy relay built around local batching and a
GitHub Actions worker. The local Go agent accepts eligible proxy requests,
queues them, sends one batch per second when work exists, and applies the
returned result package to waiting local callers.

## Goals

- Route eligible desktop HTTP(S) proxy traffic through ActionRelay.
- Minimize GitHub Actions load with one-second client-side batching.
- Let one server-side run process more than one request per package.
- Return batched responses as quickly as GitHub Actions allows.
- Keep every request bounded, auditable, and easy to fail safely.

## Components

### Local Agent

The Go agent runs on the user's machine and owns the local listener surface. It is
responsible for:

- Accepting eligible HTTP proxy requests.
- Converting traffic into request records.
- Queueing records until the next one-second batch tick.
- Enforcing method, URL, header, body, count, and byte limits.
- Sending one batch only when work is pending.
- Polling for one result package.
- Returning each result to the matching local request.

### Batch Queue

The queue groups requests by time and budget. A batch should be capped by:

- Maximum request count.
- Maximum total request bytes.
- Maximum per-request body bytes.
- Maximum time spent waiting for dispatch.
- Maximum number of outstanding batches.

### GitHub REST API

GitHub is the control plane for batch submission, workflow dispatch, run status,
and result package download. The client must handle API rate limits and eventual
consistency.

### GitHub Actions Worker

The Node.js 20 worker receives one batch package, validates it, processes the
contained requests with strict concurrency, writes one result package, and exits.

### Result Package Store

The worker writes one result package per batch to the `actionrelay-results`
branch under `results/<batch_id>.json`. The client fetches that file through
the GitHub Contents API on `api.github.com`.

## Flow

```text
1. Device traffic reaches the local route agent.
2. Agent accepts eligible requests and rejects unsupported traffic early.
3. Every second, the agent sends one batch if the queue is non-empty.
4. GitHub Actions worker validates and processes the batch.
5. Worker publishes one result package file to `actionrelay-results`.
6. Agent downloads the package from the Contents API.
7. Agent maps each result back to its waiting local request.
```

## Proxy Scope

ActionRelay is a local proxy relay for eligible request records. It should not
pretend to support every network primitive. Unsupported traffic should fail fast
with a local error instead of hanging.

Likely unsupported or restricted traffic:

- Raw TCP streams that cannot be represented as request records.
- UDP traffic.
- Websockets and long-lived streams.
- Large downloads and media streams.
- High-frequency background polling.

## Failure Model

Expected failures include:

- Local proxy/setup failure.
- Batch queue overflow.
- GitHub Actions queue delay.
- Dispatch, polling, or result publication failure.
- Request blocked by policy.
- Per-request timeout.
- Batch result package missing, malformed, or too large.

Every failure should map to a structured result entry so callers are released
quickly and do not wait forever.
