# Roadmap

ActionRelay is developed against two permanent product constraints:

- GitHub Actions is the only server-side execution environment.
- The client may connect only to `github.com` and GitHub-owned subdomains
  required for the product to function.

These constraints are architectural invariants. Every version, feature, and
platform expansion must preserve them.

## Roadmap Principles

- Keep ActionRelay GitHub-native for dispatch, execution, polling, and result
  retrieval.
- Focus first on the strongest desktop product that fits the current
  request-response architecture.
- Add new capabilities only when they can be implemented consistently across
  supported operating systems.
- Treat unsupported traffic explicitly through bypass or rejection instead of
  promising universal protocol coverage.
- Defer Android until the desktop product model is stable, documented, and
  operationally sound.

## Current Version

### v0.1

Status: delivered

Scope:

- Established the request batch and result package protocols.
- Implemented the Go client, local batching, queueing, dispatch, and polling.
- Implemented the GitHub Actions worker for batched request execution.
- Added resource controls, schema validation, security guardrails, and release
  automation.
- Moved result retrieval to a GitHub-native repository contents flow instead of
  artifact payload downloads.

Outcome:

- ActionRelay works as a GitHub Actions-backed request relay for explicit client
  requests.
- The product is functional, but it is not yet a system-wide desktop proxy.

## Planned Versions

### v0.2

Status: planned

Goal:

- Introduce the first real desktop traffic entry point for ActionRelay.

Scope:

- Add a local HTTP/HTTPS proxy daemon to the Go client.
- Support standard HTTP proxy requests and HTTPS `CONNECT` tunneling.
- Reuse the existing batch protocol, GitHub dispatcher, and worker execution
  model.
- Define the shared proxy configuration model that later desktop and Android
  work will build on.

Implementation breakdown:

1. Proxy daemon foundation
   - Add a local HTTP/HTTPS proxy daemon to the Go client.
   - Choose a stable default listen address such as `127.0.0.1:8787`.
   - Extend configuration for proxy listen settings and core runtime behavior.
2. Request handling and validation
   - Accept standard HTTP proxy requests and HTTPS `CONNECT` tunneling.
   - Normalize incoming proxy traffic into the existing ActionRelay request model.
   - Validate methods, URLs, hosts, ports, headers, bodies, redirects, and policy constraints.
3. Response handling and runtime behavior
   - Map result package entries back into proxy responses.
   - Return clear local errors when dispatch, polling, or result retrieval fails.
   - Add proxy-oriented CLI startup behavior, runtime state reporting, and readable console output.
4. Compatibility and guardrails
   - Limit `v0.2` to supported HTTP and HTTPS traffic only.
   - Reject or warn clearly for unsupported traffic such as UDP, QUIC/HTTP3, arbitrary raw TCP, and long-lived non-request-response flows.
   - Keep failure behavior explicit, fast, and safe.
5. Testing and validation
   - Add focused tests for proxy parsing, normalization, and response mapping.
   - Add end-to-end local validation for proxy request handling.
   - Manually verify browser and command-line traffic against real websites.

Scope guardrails:

- Do not add OS-wide proxy installation in `v0.2`.
- Do not add Android support in `v0.2`.
- Do not attempt transparent full-device routing in `v0.2`.
- Do not promise support for non-HTTP protocols in `v0.2`.

Expected outcome:

- Browsers and compatible applications can send traffic through the local
  ActionRelay client using standard proxy settings.

### v0.3

Status: planned

Goal:

- Make ActionRelay practical as a desktop system proxy across major desktop
  operating systems.

Scope:

- Add proxy install, uninstall, and status commands for Windows, macOS, and
  Linux.
- Add supported system proxy integration workflows for each desktop platform.
- Add standard bypass defaults for localhost, private networks, and local
  services.
- Improve status reporting for queue depth, batch state, and request handling.

Implementation breakdown:

1. Desktop integration foundation
   - Add platform-specific proxy integration flows for Windows, macOS, and Linux.
   - Define a consistent CLI surface for install, uninstall, and status operations.
   - Keep all integration behavior aligned with the existing local proxy model from `v0.2`.
2. OS proxy installation and removal
   - Implement supported commands to enable and disable ActionRelay as the local system proxy.
   - Ensure each platform updates only the settings needed for supported user traffic.
   - Add safe rollback behavior when setup is interrupted or partially applied.
3. Bypass defaults and local network safety
   - Add standard bypass defaults for localhost, loopback, private networks, and local services.
   - Prevent local machine and LAN traffic from being routed through ActionRelay by mistake.
   - Keep bypass behavior explicit and inspectable.
4. Runtime status and operator feedback
   - Improve status reporting for proxy installation state, queue depth, batch state, and request handling.
   - Surface clear operator feedback when system proxy integration is active, inactive, or partially configured.
   - Keep troubleshooting outputs practical for day-to-day desktop use.
5. Testing and platform validation
   - Validate proxy installation and removal flows on Windows, macOS, and Linux.
   - Verify bypass defaults and normal browser behavior on each supported desktop platform.
   - Confirm that `v0.3` remains within the GitHub Actions-only and GitHub-domain-only constraints.

Expected outcome:

- Desktop users can enable ActionRelay as a local system proxy with a supported,
  documented workflow.

### v0.4

Status: planned

Goal:

- Consolidate the remaining high-value desktop work into one strong product
  stabilization release.

Scope:

- Improve reliability under GitHub Actions delays, retries, and transient
  failures.
- Add fail-open and fail-closed behavior, stronger duplicate protections, and
  stale result safeguards.
- Improve observability, structured diagnostics, and troubleshooting support.
- Harden HTTPS proxy behavior, redirect handling, timeout strategy, and request
  normalization.
- Expand browser and desktop application compatibility testing.
- Improve long-running service behavior, startup behavior, cleanup, and upgrade
  safety.
- Refine policy controls, bypass rules, and operator-facing runtime insight.

Implementation breakdown:

1. Reliability and failure handling
   - Improve behavior under GitHub Actions delays, retries, and transient failures.
   - Add fail-open and fail-closed modes where appropriate.
   - Strengthen duplicate protection, stale result handling, and recovery from interrupted client state.
2. Proxy and request-path hardening
   - Harden HTTPS proxy behavior, request normalization, redirect handling, and timeout strategy.
   - Refine how supported requests move through the local proxy, batching, dispatch, and result pipeline.
   - Reduce edge-case behavior that can cause confusing or inconsistent request outcomes.
3. Observability and troubleshooting
   - Improve structured diagnostics, logs, and runtime visibility for operators.
   - Make it easier to understand queue pressure, dispatch timing, result latency, and failure reasons.
   - Strengthen troubleshooting support in both CLI behavior and technical documentation.
4. Compatibility and long-running service behavior
   - Expand browser and desktop application compatibility testing.
   - Improve startup behavior, cleanup behavior, and general long-running service stability.
   - Refine policy controls, bypass behavior, and operator-facing runtime insight for daily use.
5. Validation and release readiness
   - Re-test the desktop product across supported operating systems after stabilization changes.
   - Confirm that reliability, observability, and compatibility improvements remain within the GitHub-native architecture.
   - Close the highest-value gaps blocking the move toward a stable desktop release.

Expected outcome:

- ActionRelay becomes a serious, supportable desktop product for supported
  HTTP/HTTPS traffic within the GitHub-native architecture.

### v0.5

Status: planned

Goal:

- Prepare the platform for a stable `v1.0` desktop release.

Scope:

- Finalize stable defaults for queueing, batching, limits, retries, and policy
  behavior.
- Lock down the configuration and protocol compatibility expectations for the
  `v1.x` line.
- Complete documentation for installation, operations, compatibility, and
  recovery.
- Close major desktop regressions and compatibility gaps discovered during
  stabilization.

Implementation breakdown:

1. Stable defaults and product behavior
   - Finalize production-ready defaults for queueing, batching, limits, retries, and policy behavior.
   - Remove or tighten behaviors that are still too experimental for a stable desktop release path.
   - Make the default runtime profile predictable for everyday use.
2. Configuration and protocol release contract
   - Lock down configuration expectations for the `v1.x` line.
   - Confirm protocol compatibility expectations between the client and the GitHub Actions worker.
   - Reduce avoidable breaking changes before the stable desktop release.
3. Documentation and operator readiness
   - Complete installation, operations, compatibility, and recovery documentation.
   - Improve operator guidance for setup, troubleshooting, upgrades, and expected limitations.
   - Ensure the documentation reflects the real supported desktop product model.
4. Regression closure and release polish
   - Close the highest-priority desktop regressions and compatibility gaps discovered during stabilization.
   - Refine the release experience so the product behaves consistently across supported desktop platforms.
   - Confirm that remaining limitations are clearly documented rather than left ambiguous.
5. Final validation before `v1.0`
   - Re-validate the desktop release candidate across Windows, macOS, and Linux.
   - Confirm that the stable desktop product still respects the GitHub Actions-only and GitHub-domain-only constraints.
   - Use this release to establish a clear boundary for the first stable desktop version.

Expected outcome:

- The desktop product reaches a stable and clearly documented release boundary.

### v1.0

Status: planned

Goal:

- Ship the first stable desktop release of ActionRelay.

Scope:

- Stabilize Windows, macOS, and Linux around one supported product model.
- Publish explicit support boundaries, limitations, and troubleshooting
  guidance.
- Treat the GitHub Actions-only and GitHub-domain-only constraints as permanent
  release criteria.

Implementation breakdown:

1. Stable release packaging and delivery
   - Finalize the desktop release shape for Windows, macOS, and Linux.
   - Ensure binaries, release assets, and documentation are aligned with the supported product model.
   - Make the stable release easy to install, run, and verify on supported desktop platforms.
2. Supported product boundary
   - Publish the definitive support boundaries, limitations, and non-goals for the stable desktop release.
   - Make the supported HTTP/HTTPS use cases clear and keep unsupported traffic behavior explicit.
   - Ensure the stable release is described as a GitHub-native desktop proxy, not as a universal tunnel.
3. Operational stability and supportability
   - Confirm that the desktop client behaves consistently in normal long-running use.
   - Ensure runtime status, diagnostics, and recovery guidance are sufficient for practical operation.
   - Treat predictability and supportability as release requirements, not optional polish.
4. Invariant enforcement
   - Treat the GitHub Actions-only and GitHub-domain-only constraints as permanent release gates.
   - Confirm that no release path depends on non-GitHub execution or non-GitHub result transport.
   - Preserve the core architectural rules in code, documentation, and release behavior.
5. Final stable release validation
   - Re-validate the full desktop product on Windows, macOS, and Linux.
   - Confirm that release assets, setup instructions, and documented limitations match actual behavior.
   - Use `v1.0` to establish the first stable desktop baseline for future versions.

Expected outcome:

- ActionRelay is a stable GitHub-native desktop proxy for supported HTTP/HTTPS
  traffic.

### v1.1

Status: planned

Goal:

- Introduce Android only after the desktop model is stable enough to extend.

Scope:

- Build the first Android client around the same GitHub-native protocol and
  dispatch model.
- Design Android traffic capture around platform-standard mechanisms while
  preserving the project invariants.
- Reuse the shared configuration model wherever Android constraints allow.
- Document Android-specific setup, limitations, permissions, and compatibility
  boundaries.

Expected outcome:

- ActionRelay expands beyond desktop with a first Android implementation built
  on the same core architectural rules.

## Long-Term Direction

After the initial Android release, future versions should continue along these
paths without breaking the project invariants:

- improve Android reliability, lifecycle handling, and long-running behavior
- broaden validated compatibility across desktop and Android applications
- strengthen policy controls, observability, and operational tooling
- refine proxy behavior for supported HTTP/HTTPS traffic classes
- improve upgrade safety, configuration migration, and release discipline

## Practical Ceiling

Under the permanent constraints of this project, the realistic end-state is not
an arbitrary-packet VPN or a universal transport tunnel. The most mature
practical outcome is:

- a cross-platform local client for desktop and Android,
- backed only by GitHub Actions,
- using only GitHub-owned domains for client egress,
- focused on supported HTTP/HTTPS traffic,
- with explicit bypass or rejection for unsupported traffic,
- and with enough operational maturity for daily long-running use.

That ceiling still allows many future releases, but all of them must remain
inside the same GitHub-native product model.

## Release Discipline

Before promoting any roadmap item to a shipped version:

- Keep the server-side execution path on GitHub Actions only.
- Keep client egress restricted to GitHub-owned domains required by the design.
- Update technical documentation for setup, limits, compatibility, and
  troubleshooting.
- Validate the feature against the exact dependency versions used by the
  project.
