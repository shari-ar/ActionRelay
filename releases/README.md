# Release Assets

Phase 5 release automation publishes cross-platform `actionrelay` client binaries and
checksums for each version tag.

## Asset Naming

Release archives follow this pattern:

- `actionrelay_<version>_linux_amd64.tar.gz`
- `actionrelay_<version>_linux_arm64.tar.gz`
- `actionrelay_<version>_darwin_amd64.tar.gz`
- `actionrelay_<version>_darwin_arm64.tar.gz`
- `actionrelay_<version>_windows_amd64.zip`
- `actionrelay_<version>_windows_arm64.zip`
- `SHA256SUMS.txt`

## Checksum Verification

Linux and macOS (with `sha256sum`):

```sh
sha256sum -c SHA256SUMS.txt
```

macOS fallback (with `shasum`):

```sh
shasum -a 256 -c SHA256SUMS.txt
```
