# Security

ActionRelay routes whole-device eligible traffic, so it may see cookies,
authorization headers, form data, and private responses. The design must protect
sensitive values while keeping traffic bounded.

## Trust Model

- The local route agent is trusted by the user.
- GitHub Actions runs the reviewed worker code.
- Remote sites and responses are untrusted.
- Workflow logs and published result files must not expose secrets unnecessarily.

## Sensitive Traffic

The system should:

- Avoid logging request bodies and sensitive headers.
- Redact cookies, authorization headers, and set-cookie values.
- Keep result packages compact and short-lived.
- Provide domain allowlists or blocklists.
- Let users exclude sensitive apps or destinations from routing.

## Local Route Guardrails

The route agent should require local authorization for route install/uninstall,
bind any control API to loopback only, and fail closed if the route cannot be
removed cleanly.

Current implementation safeguards:

- Enforces loopback-only `agent_listen_addr` validation.
- Marks route state as `cleanup_required` when the agent stops while route mode
  remains installed.
- Clears stale cleanup flags when a healthy local agent starts.

## URL And Network Guardrails

The worker must revalidate every request and reject unsafe destinations:

- Non-HTTP(S) schemes unless explicitly supported.
- Localhost and loopback targets.
- Link-local and private IP ranges unless explicitly allowed.
- Cloud metadata service addresses.
- Redirects into blocked destinations.

Current implementation safeguards:

- Validates request-batch shape before processing.
- Rejects localhost, loopback, private, link-local, multicast, and metadata
  service destinations.
- Resolves DNS before fetch and rejects requests when any resolved address is
  blocked.
- Follows redirects manually and reapplies destination guardrails on each hop.

## Abuse Prevention

Recommended controls:

- One batch per second when work is pending.
- Strict batch size and concurrency limits.
- Short workflow timeouts.
- Small response caps.
- Clear audit trail through batch IDs and workflow runs.

Current implementation safeguards:

- Redacts sensitive response headers (`set-cookie`, authentication challenge
  headers) before writing result packages.
- Redacts common credential and token patterns from worker and client error
  messages.
- Verifies result package structure, request ID coverage, and batch ID match
  before releasing responses to local callers.
