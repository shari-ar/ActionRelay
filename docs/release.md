# Release Automation

ActionRelay Phase 5 uses GitHub Actions to build and publish versioned client
binaries with checksums.

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

## CI Coverage

The CI workflow validates:

- Go client compilation and test execution
- Go formatting (`gofmt`)
- Worker script syntax and smoke behavior
- Schema file structure and protocol constants
- Documentation integrity checks

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
