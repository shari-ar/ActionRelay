# Protocol

ActionRelay uses compact JSON packages between the local route agent and the
GitHub Actions worker. The client sends request batches; the worker returns one
result package for each batch.

## Request Batch

Target schema path: `schemas/request-batch.v1.json`

```json
{
  "protocol": "actionrelay.request_batch.v1",
  "batch_id": "01J00000000000000000000000",
  "sent_at": "2026-05-10T00:00:00Z",
  "client": {
    "batch_interval_ms": 1000,
    "route_mode": "whole_device"
  },
  "limits": {
    "max_response_bytes_per_request": 65536,
    "request_timeout_ms": 8000,
    "worker_concurrency": 4
  },
  "requests": [
    {
      "request_id": "req-1",
      "method": "GET",
      "url": "https://example.com/",
      "headers": {
        "accept": "text/html"
      },
      "body": null
    }
  ]
}
```

## Result Package

Target schema path: `schemas/result-package.v1.json`

```json
{
  "protocol": "actionrelay.result_package.v1",
  "batch_id": "01J00000000000000000000000",
  "ok": true,
  "results": [
    {
      "request_id": "req-1",
      "ok": true,
      "response": {
        "status": 200,
        "headers": {
          "content-type": "text/html"
        },
        "body": {
          "encoding": "utf8",
          "data": "<html></html>",
          "bytes": 13,
          "truncated": false
        },
        "url": "https://example.com/",
        "timing_ms": 620
      },
      "error": null
    }
  ]
}
```

## Batch Rules

- The client sends at most one batch per second when the queue is non-empty.
- A batch may contain one or many requests.
- Every request must have a unique `request_id` within the batch.
- Batch byte size and request count must be capped before upload.
- The worker must revalidate the full batch before processing.
- The result package must include a result or error for every accepted request.

## Error Codes

Initial error codes:

- `ROUTE_UNSUPPORTED`: Traffic cannot be represented as a request record.
- `QUEUE_OVERFLOW`: Local queue exceeded configured limits.
- `BATCH_TOO_LARGE`: Batch exceeded count or byte limits.
- `REQUEST_BLOCKED`: Policy blocked the request.
- `URL_REJECTED`: URL failed validation.
- `METHOD_REJECTED`: Method is not allowed.
- `HEADER_REJECTED`: Header is not allowed.
- `BODY_TOO_LARGE`: Request body exceeds the cap.
- `TIMEOUT`: Request timed out.
- `RESPONSE_TOO_LARGE`: Response exceeded the cap.
- `DISPATCH_FAILED`: GitHub workflow dispatch failed.
- `RUN_DELAYED`: Workflow did not start quickly enough.
- `RESULT_PACKAGE_NOT_FOUND`: Result package was unavailable.
- `WORKER_ERROR`: Worker failed unexpectedly.

## Result Storage Format

Each batch produces one result file in the results branch:

```text
branch: actionrelay-results
path:   results/<batch_id>.json
```

The client reads that JSON file using the GitHub Contents API and must reject
malformed, oversized, or mismatched result packages.
