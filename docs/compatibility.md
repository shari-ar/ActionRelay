# Compatibility

This document describes supported behavior for the desktop product model.

## Supported Platforms

Current supported desktop operating systems:

- Windows
- macOS
- Linux

## Supported Traffic Model

ActionRelay supports bounded request/response HTTP(S) traffic represented in the
request-batch protocol.

Supported:

- Standard HTTP proxy requests.
- Explicit HTTP requests and bounded HTTPS-origin responses that fit the batch model.
- Browser and desktop app traffic that behaves like short-lived HTTP(S)
  requests, provided it does not require CONNECT tunneling.

Not supported:

- `CONNECT` tunneling in current local proxy mode.
- UDP traffic.
- Arbitrary raw TCP tunneling.
- QUIC/HTTP3 native transport paths.
- Long-lived bidirectional streaming protocols.

## Architectural Compatibility Invariants

All supported operation must preserve:

- GitHub Actions as the only server-side execution environment.
- Client connectivity restricted to `github.com` and required GitHub-owned
  subdomains for control/result flow.

## Release-Line Compatibility

For `v1.x` stability planning:

- `config_version` is `1`.
- Request protocol remains `actionrelay.request_batch.v1`.
- Result protocol remains `actionrelay.result_package.v1`.

Any incompatible future changes must introduce explicit new versions and
migration guidance.
