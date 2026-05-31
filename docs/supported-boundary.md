# Supported Boundary

This document is the definitive support boundary for the stable desktop release.

## Product Identity

ActionRelay is a GitHub-native desktop HTTP(S) proxy relay. It is not a
universal network tunnel.

## Supported Use Cases

- Desktop applications and browsers that issue bounded HTTP(S)
  request/response traffic.
- Local operation on Windows, macOS, and Linux with documented proxy/route
  lifecycle commands.
- GitHub Actions-backed execution and GitHub-native result retrieval.

## Non-Goals

- Unlimited VPN behavior.
- Raw packet forwarding.
- Arbitrary TCP or UDP tunneling.
- QUIC/HTTP3 native transport support.
- Long-lived bidirectional streaming protocol support.

## Permanent Invariants

- Server-side execution is GitHub Actions only.
- Client control/result transport is restricted to GitHub-owned domains required
  by the product model.
- Protocol contract remains on the stable `v1` request/result envelope pair
  until a deliberate version migration is introduced.
