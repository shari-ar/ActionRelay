# Roadmap

## Phase 0: Documentation

- Define whole-device route scope.
- Document one-second batching.
- Document batch protocol and resource limits.

## Phase 1: Minimal Batch Flow

- Build Go route agent skeleton.
- Add local request queue.
- Add one-second batch sender.
- Add GitHub workflow dispatch and polling.
- Add Node.js batch worker.
- Return one result package to the client.

## Phase 2: Route Integration

- Add route install and uninstall commands.
- Add request classification.
- Add local error responses for unsupported traffic.
- Add status commands for queue and batch state.

## Phase 3: Resource Controls

- Add cache and deduplication.
- Add strict batch size limits.
- Add worker concurrency limits.
- Add backpressure for delayed GitHub Actions runs.

## Phase 4: Security Hardening

- Add schema validation.
- Add URL and redirect guardrails.
- Add secret redaction.
- Add result package verification.
- Add route cleanup safeguards.

## Phase 5: Release Automation

- Build Go binaries.
- Package checksums.
- Add CI for client, worker, schemas, and docs.
- Publish versioned releases.
