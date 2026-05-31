# ActionRelay

ActionRelay is a lightweight whole-device network route relay. A local Go client routes
eligible device traffic into a small request queue, sends pending requests to the
server side once per second, and receives compact batched results back.

The server side is a GitHub Actions based worker/control plane. It can receive a
batch containing more than one client request, process the batch, and return one
result package to the client.

## What It Is

- A local Go route agent for whole-device eligible HTTP(S) requests.
- A one-second batching loop that sends work only when requests are pending.
- A GitHub REST API control plane for upload, dispatch, polling, and results.
- A Node.js 20 worker that processes request batches with strict limits.
- A compact JSON protocol for request batches and result packages.

## What It Is Not

- Not an unlimited VPN service.
- Not a raw packet tunnel.
- Not an anonymous network.
- Not intended for streaming, bulk downloads, or high-volume scraping.

ActionRelay behaves like a constrained whole-device network route, not a general
TCP/UDP tunnel. Traffic must be converted into bounded request records before it
can be batched and relayed.

ActionRelay is positioned as a GitHub-native desktop HTTP(S) proxy relay, not a
universal tunnel. The definitive support boundary is documented in
`docs/supported-boundary.md`.

## Core Flow

1. The local agent captures eligible whole-device requests.
2. Requests are queued locally for the next batch tick.
3. Once per second, if the queue is non-empty, the client sends one batch package.
4. The GitHub Actions worker processes all requests in that batch.
5. The worker writes one compact result package.
6. The client downloads the package and releases responses to local callers.

```text
device traffic -> local route agent -> 1s request batch -> GitHub Actions worker
       ^                                                               |
       |                                                               v
       +--------------- batched responses <- result package <----------+
```

## Batch Model

The client does not dispatch every single request immediately. It groups pending
requests into one package every second. This reduces GitHub API calls, workflow
work, artifact churn, and polling overhead.

A batch may contain one request or many requests. Each request has its own ID,
limits, timeout, headers, body metadata, and result entry. The server returns one
package containing all completed results and structured errors.

## Resource Rules

- Send at most one outbound batch per second when work is pending.
- Keep batches small and bounded by request count and byte size.
- Limit concurrent server-side fetches inside each batch.
- Cache safe responses locally where possible.
- Drop, block, or fail unsupported traffic quickly.
- Keep workflows and artifacts short-lived.
- Prefer clear local errors over long waits.

## Planned Repository Layout

```text
.
|-- client/                 # Go whole-device route agent and CLI controls
|-- worker/                 # Node.js 20 batch worker
|-- schemas/                # JSON schemas for batches and results
|-- protocol/               # Protocol examples and versioning
|-- .github/workflows/      # GitHub Actions workflows
|-- tests/                  # Unit, integration, and route-flow tests
|-- docs/                   # User and maintainer documentation
|-- scripts/                # Local helper scripts
|-- releases/               # Release notes and packaging metadata
+-- README.md
```

## Intended Usage

Command names may change during implementation, but the target flow is:

```sh
actionrelay init --repo owner/repo --workflow actionrelay.yml
actionrelay route install --yes --config .actionrelay/config.json
actionrelay serve --config .actionrelay/config.json
actionrelay status --config .actionrelay/config.json
actionrelay fetch https://api.example.com/status
```

Manual fetch mode is available for testing:

```sh
actionrelay fetch https://api.example.com/status
```

## Documentation

- `docs/README.md` is the documentation index.
- `docs/architecture.md` explains the whole-device route and batch design.
- `docs/protocol.md` defines batch request and result package envelopes.
- `docs/usage.md` describes local route setup and operation.
- `docs/compatibility.md` describes supported platforms and traffic model.
- `docs/supported-boundary.md` defines stable boundary and non-goals.
- `docs/limitations.md` lists explicit unsupported behavior.
- `docs/performance.md` defines resource budgets and latency tradeoffs.
- `docs/security.md` covers local routing, sensitive traffic, and guardrails.
- `docs/development.md` outlines contributor conventions.
- `docs/release.md` describes CI and versioned release automation.
- `docs/roadmap.md` tracks implementation phases.

## Release Assets

Stable release assets are published as `.tar.gz` archives for all supported
desktop targets, plus `SHA256SUMS.txt` and `RELEASE_MANIFEST.txt` for
verification.

## Project Status

ActionRelay is in the documentation and design phase. The first implementation
milestone is a minimal local-route-to-queued-batch-to-GitHub-Actions flow with
strict limits and clear errors.

## License

No license has been selected yet. Do not assume redistribution rights until a
license file is added.
