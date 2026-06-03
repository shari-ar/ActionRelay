# Release Automation

ActionRelay uses GitHub Actions to build and publish versioned client binaries
with checksums.

## Versioning Model

- A GitHub release publication event triggers packaging and asset upload.
- The release tag value is embedded in artifact names.
- If needed, maintainers can manually rerun the release workflow with
  `workflow_dispatch` and an existing tag.

## Build Outputs

The release workflow builds `actionrelay` from `client/cmd/actionrelay` for:

- Linux (`amd64`, `arm64`)
- macOS (`amd64`, `arm64`)
- Windows (`amd64`, `arm64`)

Each artifact includes:

- The platform-specific binary
- `README.md`

A `SHA256SUMS.txt` file is generated for all archives.
A `RELEASE_MANIFEST.txt` file is generated to describe release packaging shape.

Stable release packaging format:

- All platforms ship as `.tar.gz` archives (including Windows).
- Release uploads include:
  - `actionrelay_<version>_<goos>_<goarch>.tar.gz`
  - `SHA256SUMS.txt`
  - `RELEASE_MANIFEST.txt`

## CI Coverage

The CI workflow validates:

- Go client compilation and test execution
- Go formatting (`gofmt`)
- Worker script syntax and smoke behavior
- Schema file structure and protocol constants

## Authentication Model

Release and CI automation do not require a custom repository secret for the
ActionRelay GitHub token.

- GitHub Actions workflows in this repository use the built-in `GITHUB_TOKEN`.
- The local ActionRelay client uses a user-managed personal access token through
  the `ACTIONRELAY_GITHUB_TOKEN` environment variable.
- Token creation and operating-system-specific setup steps are documented in
  `usage.md`.

## Release Flow

1. Create or publish a semantic version release in GitHub (for example
   `v1.2.3`).
2. Workflow builds cross-platform archives and checksum file for that release
   tag.
3. Workflow uploads assets directly to the published GitHub Release.

## Release Readiness Validation Gate

ActionRelay now includes an explicit release-readiness validation gate in CI:

- Workflow step: `Validate release readiness invariants`
- Script: `scripts/ci/validate-release-readiness.mjs`

This gate verifies:

- CI and release workflows still derive Go version from `client/go.mod`.
- Client `go test ./...` and `gofmt` checks remain enforced in CI.
- Release workflow still builds cross-platform assets and uploads checksums.
- Protocol/schema invariants required by the desktop client remain unchanged.
- Operator guidance exists for operations, compatibility, and recovery.
- Explicit product limitations remain documented and indexed.
- Final pre-`v1.0` desktop validation checklist is documented and indexed.
- Stable support boundary/non-goal documentation remains present and indexed.

This keeps desktop stabilization changes within the GitHub-native architecture
and helps catch regressions before tagging a release.

## Pre-v1.0 Validation

Before `v1.0`, run the desktop final validation process documented in
`final-validation.md`.
