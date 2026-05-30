# Final Validation

This checklist defines the required pre-`v1.0` desktop validation pass.

## Scope

Validation must confirm:

- Desktop behavior is stable on Windows, macOS, and Linux.
- Release artifacts, setup instructions, and limitations are aligned.
- GitHub Actions-only and GitHub-domain-only invariants remain intact.

## Platform Validation Checklist

Perform the following on each supported desktop OS:

1. Install latest release binary for the platform.
2. Set `ACTIONRELAY_GITHUB_TOKEN`.
3. Run:

```sh
actionrelay init --repo owner/repo --workflow actionrelay.yml
actionrelay fetch https://api.github.com
actionrelay serve --config .actionrelay/config.json
actionrelay status --config .actionrelay/config.json
```

4. Confirm:
   - `agent.reachable=true`
   - `diagnostics.severity=ok` during healthy operation
   - no unexpected `diagnostics.issues`
5. Validate proxy install/uninstall lifecycle commands on that OS.
6. Validate recovery procedures from `recovery.md`.

## Invariant Validation Checklist

- Protocol constants remain:
  - `actionrelay.request_batch.v1`
  - `actionrelay.result_package.v1`
- `config_version=1` contract is enforced.
- Documentation keeps explicit product limitations and supported boundaries.
- Release workflow still builds and uploads expected assets.

## Release Readiness Exit Criteria

`v1.0` candidate is ready only when:

- All CI jobs pass.
- This checklist has been completed for Windows, macOS, and Linux.
- No undocumented behavior gaps remain between implementation and docs.
