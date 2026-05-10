# Usage

ActionRelay is intended to run as a local whole-device route agent. Eligible
requests are queued locally, sent to the server side once per second, processed
as a batch, and returned as one result package.

## Prerequisites

- GitHub repository with the ActionRelay workflow.
- GitHub Actions enabled.
- GitHub token for workflow dispatch, run read, and artifact read.
- Local ActionRelay Go route agent.
- Permission to install or configure a local route on the device.

## Start The Route Agent

Target commands:

```sh
actionrelay init --repo owner/repo --workflow actionrelay.yml
actionrelay serve --config .actionrelay/config.json
```

## Runtime Behavior

The agent should:

- Capture eligible whole-device requests.
- Queue requests locally.
- Send one batch every second if the queue is non-empty.
- Receive one result package for the batch.
- Match each result to the original local request.
- Fail unsupported traffic quickly with a local error.

## Local Agent Endpoints

Phase 1 exposes a local API from `serve`:

- `GET /healthz` for readiness checks.
- `POST /v1/requests` for request submission.

`POST /v1/requests` returns one request result object after the request is
processed within a batch cycle.

## Manual Test Mode

Manual fetch mode remains useful for testing without installing the route:

```sh
actionrelay fetch https://api.example.com/status
```

Phase 1 does not include route install or uninstall commands yet. Those are
planned for Phase 2.

## User Expectations

The one-second batch tick reduces resource usage but adds a small local wait
before dispatch. GitHub Actions can also add queueing, runner startup, artifact,
and polling delays. ActionRelay should be fast for small bounded requests, but it
should not promise real-time VPN latency.
