# Operations

This guide defines day-to-day operation of the stable desktop ActionRelay
client.

## Standard Runtime

1. Ensure `ACTIONRELAY_GITHUB_TOKEN` is set in the runtime environment.
2. Verify config file exists and is valid:
   - `.actionrelay/config.json`
3. Start the local service:

```sh
actionrelay serve --config .actionrelay/config.json
```

4. Validate health and runtime status:

```sh
actionrelay status --config .actionrelay/config.json
```

## Operator Checks

Review these status areas first:

- `agent.reachable`: must be `true`.
- `diagnostics.severity`: should be `ok` in normal operation.
- `diagnostics.issues`: should be empty in normal operation.
- `agent.runtime.queue_depth`: should stay well below queue capacity.
- `agent.runtime.last_dispatch_error_code`: empty in healthy state.

## Long-Running Stability Checks

For continuously running deployments, perform this checklist at least once per
shift (or every 4-8 hours):

- `supportability.health_class`: should remain `healthy`.
- `supportability.needs_attention`: should remain `false`.
- `supportability.failure_ratio`: should remain below `0.30` over time.
- `agent.runtime.last_result_at`: should refresh regularly during active use.
- `agent.runtime.dispatch_in_flight`: should not stay `true` for multiple minutes.

If any check fails repeatedly, use `docs/recovery.md` and capture one `status`
output sample before restarting the service.

## Proxy Lifecycle

Install system proxy integration:

```sh
actionrelay proxy install --yes --config .actionrelay/config.json
```

Check proxy platform/runtime state:

```sh
actionrelay proxy status --config .actionrelay/config.json
```

Remove system proxy integration:

```sh
actionrelay proxy uninstall --yes --config .actionrelay/config.json
```

## Route Lifecycle

Install route state:

```sh
actionrelay route install --yes --config .actionrelay/config.json
```

Remove route state:

```sh
actionrelay route uninstall --yes --config .actionrelay/config.json
```

## Upgrade Procedure

1. Stop running `serve`.
2. Replace binary with new release build.
3. Keep existing `.actionrelay/config.json` unless migrating across a future
   config version boundary.
4. Restart `serve`.
5. Re-run `status` and verify `diagnostics.severity=ok`.
