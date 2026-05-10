# Security

ActionRelay routes whole-device eligible traffic, so it may see cookies,
authorization headers, form data, and private responses. The design must protect
sensitive values while keeping traffic bounded.

## Trust Model

- The local route agent is trusted by the user.
- GitHub Actions runs the reviewed worker code.
- Remote sites and responses are untrusted.
- Workflow logs and artifacts must not expose secrets unnecessarily.

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

## URL And Network Guardrails

The worker must revalidate every request and reject unsafe destinations:

- Non-HTTP(S) schemes unless explicitly supported.
- Localhost and loopback targets.
- Link-local and private IP ranges unless explicitly allowed.
- Cloud metadata service addresses.
- Redirects into blocked destinations.

## Abuse Prevention

Recommended controls:

- One batch per second when work is pending.
- Strict batch size and concurrency limits.
- Short workflow timeouts.
- Small response caps.
- Clear audit trail through batch IDs and workflow runs.
