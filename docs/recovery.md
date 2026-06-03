# Recovery

This guide provides deterministic recovery actions for common operational
failures.

## Service Not Reachable

Symptoms:

- `actionrelay status` reports `agent.reachable=false`.
- `diagnostics.issues` contains `agent_unreachable`.

Recovery:

1. Start service:

```sh
actionrelay serve --config .actionrelay/config.json
```

2. Re-check:

```sh
actionrelay status --config .actionrelay/config.json
```

## Backpressure Or Delayed Runs

Symptoms:

- `diagnostics.issues` contains `backpressure_active` or `RUN_DELAYED`.

Recovery:

1. Wait for backpressure cooldown window to pass.
2. Confirm GitHub Actions workflow capacity and repository Actions health.
3. Re-run a simple test:

```sh
actionrelay fetch https://api.github.com
```

## Dispatch Or Result Failures

Symptoms:

- `agent.runtime.last_dispatch_error_code` is set.
- `diagnostics.issues` includes `last_dispatch_error:*`.

Recovery:

1. Verify token is valid and not expired.
2. Verify repository/workflow name in `.actionrelay/config.json`.
3. Verify `actionrelay-results` branch exists and is writable by workflow.
4. Restart `serve` and re-test with `fetch`.

## Long-Running Degradation

Symptoms:

- `supportability.health_class` is `degraded` or `unhealthy`.
- `supportability.needs_attention=true`.
- `supportability.recommended_actions` includes recovery steps.

Recovery:

1. Run and save a fresh status output:

```sh
actionrelay status --config .actionrelay/config.json
```

2. Execute recommended actions in order:
   - `check_serve_process` / `verify_agent_listen_addr`
   - `verify_github_token_and_workflow_access`
   - `run_fetch_smoke_test`
3. If degradation persists for 15+ minutes, restart `serve`.
4. If still unresolved, rotate token, re-check workflow permissions, and
   collect logs + status output for escalation.

## Route Cleanup Required

Symptoms:

- Route state indicates `cleanup_required=true`.

Recovery:

1. Run uninstall:

```sh
actionrelay route uninstall --yes --config .actionrelay/config.json
```

2. Confirm cleanup flags are cleared with `status`.

## Result Storage Growth

Symptoms:

- The `actionrelay-results` branch keeps growing unexpectedly.

Recovery:

1. Verify the scheduled cleanup workflow is enabled:
   - `.github/workflows/actionrelay-results-retention.yml`
2. Confirm recent cleanup workflow runs are succeeding in GitHub Actions.
3. If needed, run the cleanup workflow manually with `workflow_dispatch`.
4. Confirm old files under `results/` are removed from `actionrelay-results`.

## Proxy Misconfiguration

Symptoms:

- Desktop traffic no longer routes correctly after install/uninstall.

Recovery:

1. Remove proxy integration:

```sh
actionrelay proxy uninstall --yes --config .actionrelay/config.json
```

2. Reinstall proxy integration:

```sh
actionrelay proxy install --yes --config .actionrelay/config.json
```

3. Re-check proxy runtime:

```sh
actionrelay proxy status --config .actionrelay/config.json
```
