# Limitations

This document is the authoritative list of current desktop product limitations.

## Core Architectural Limits

- Server-side execution is GitHub Actions only.
- Client control/result flow is GitHub-domain constrained (`github.com` and
  required GitHub-owned subdomains).

## Traffic And Protocol Limits

- Supported model is bounded request/response HTTP(S) traffic.
- `CONNECT` tunneling is not supported in current local proxy mode.
- CONNECT tunneling is not supported.
- UDP and arbitrary raw TCP tunneling are not supported.
- QUIC/HTTP3 native transport paths are not supported.
- Long-lived bidirectional streaming protocols are not supported.

## Performance And Runtime Limits

- Requests are dispatched in one-second batch cycles when queue is non-empty.
- Latency includes GitHub Actions queue/startup/execution/result publication
  overhead.
- Request/response size and timeout limits are strictly enforced by config
  guardrails.

## Operational Limits

- Desktop proxy integration support is limited to Windows, macOS, and Linux.
- Behavior depends on correct local proxy/route installation state.
- Network-policy failures and GitHub transient failures may surface as explicit
  request errors rather than transparent retries.
