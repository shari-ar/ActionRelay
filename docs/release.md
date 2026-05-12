# Release Automation

ActionRelay Phase 5 uses GitHub Actions to build and publish versioned client
binaries with checksums.

## Versioning Model

- A Git tag in the form `v<major>.<minor>.<patch>` triggers release publishing.
- The tag value is embedded in artifact names.
- GitHub auto-generated release notes are published for each tag.

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

## Release Flow

1. Push a semantic version tag, for example `v1.2.3`.
2. Workflow builds cross-platform archives and checksum file.
3. Workflow uploads assets to the GitHub Release for the tag.
