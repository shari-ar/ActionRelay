# Development

ActionRelay should stay small and explicit. The local route agent must reduce
work before spending GitHub Actions capacity.

## Planned Layout

```text
client/                 Go route agent, batching, and CLI controls
worker/                 Node.js 24 batch worker
schemas/                JSON schemas
protocol/               Protocol examples and compatibility notes
.github/workflows/      Workflow definitions
tests/                  Unit, integration, and route-flow tests
docs/                   Documentation
scripts/                Developer helpers
releases/               Release metadata
```

## Implementation Rules

- Keep whole-device capture and batching in the local Go agent.
- Send at most one batch per second when work is pending.
- Keep outbound internet fetches in the worker.
- Validate batches in both client and worker.
- Treat GitHub APIs as eventually consistent.
- Prefer clear local errors over hanging requests.
- Do not silently support unsupported raw traffic.

## Test Strategy

Recommended tests:

- Route policy tests for supported and unsupported traffic.
- Batch queue tests for timing, count limits, and byte limits.
- Worker validation tests for URL, method, header, and body limits.
- Protocol schema tests.
- Result package parsing tests.
- Negative tests for oversized batches, timeouts, and missing results.

## CI Automation

GitHub Actions workflow `.github/workflows/ci.yml` validates:

- Go client build and tests from `client/`.
- Go formatting consistency via `gofmt`.
- Worker syntax and smoke behavior checks.
- Schema structure validation in `schemas/`.

## Version Maintenance

Developers working on ActionRelay should keep the versions of the tools used by
the project up to date as part of normal maintenance work.

Review and update these version groups deliberately:

- Go / Node versions
  - `client/go.mod`
  - `worker/package.json`
  - `.github/workflows/ci.yml`
  - `.github/workflows/actionrelay.yml`
  - `.github/workflows/release.yml`
- GitHub Actions versions
  - `actions/checkout`
  - `actions/setup-go`
  - `actions/setup-node`
  - `actions/github-script`
  - `actions/upload-artifact`
- Pinned Runtime / API Versions
  - GitHub REST API version in `client/internal/githubapi/dispatcher.go`

When changing any of these versions:

- Keep the code, workflows, and technical documentation in sync.
- Re-run the relevant CI and release-readiness checks.
- Verify that any pinned protocol or API version remains compatible with the
  current implementation.

## Release Automation

GitHub Actions workflow `.github/workflows/release.yml` publishes versioned
releases from semantic version tags.

- Push a tag formatted like `v1.2.3` to trigger a release.
- Cross-platform client binaries are built from `client/cmd/actionrelay`.
- Release archives and `SHA256SUMS.txt` are attached to the GitHub Release.

Local release build helper:

```sh
./scripts/release/build-client-binaries.sh v1.2.3
```
