import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

async function main() {
  const repoRoot = process.cwd();
  const tempDir = await mkdtemp(path.join(os.tmpdir(), "actionrelay-worker-smoke-"));
  try {
    const resultPath = path.join(tempDir, "result-package.json");

    const batchPayload = {
      protocol: "actionrelay.request_batch.v1",
      batch_id: "smoke-batch",
      sent_at: "2026-01-01T00:00:00Z",
      client: {
        batch_interval_ms: 1000,
        route_mode: "whole_device",
      },
      limits: {
        max_response_bytes_per_request: 1024,
        request_timeout_ms: 1000,
        worker_concurrency: 1,
      },
      requests: [
        {
          request_id: "req-smoke-1",
          method: "TRACE",
          url: "https://example.com/",
          body: null,
        },
      ],
    };

    const encodedBatch = Buffer.from(JSON.stringify(batchPayload)).toString("base64");
    const workerPath = path.join(repoRoot, "worker", "run-batch.mjs");

    const run = spawnSync(process.execPath, [workerPath], {
      cwd: repoRoot,
      encoding: "utf8",
      env: {
        ...process.env,
        ACTIONRELAY_BATCH_B64: encodedBatch,
        ACTIONRELAY_RESULT_PATH: resultPath,
        ACTIONRELAY_MAX_RESPONSE_BYTES: "1024",
        ACTIONRELAY_REQUEST_TIMEOUT_MS: "1000",
        ACTIONRELAY_WORKER_CONCURRENCY: "1",
      },
    });

    if (run.status !== 0) {
      throw new Error(`worker smoke run failed: ${run.stderr || run.stdout}`);
    }

    const raw = await readFile(resultPath, "utf8");
    const parsed = JSON.parse(raw);

    if (parsed.protocol !== "actionrelay.result_package.v1") {
      throw new Error("unexpected result protocol in worker smoke output");
    }
    if (!Array.isArray(parsed.results) || parsed.results.length !== 1) {
      throw new Error("worker smoke output must contain one result");
    }

    const result = parsed.results[0];
    if (result.ok !== false || !result.error || result.error.code !== "METHOD_REJECTED") {
      throw new Error("worker smoke output did not reject unsupported method as expected");
    }
  } finally {
    await rm(tempDir, { recursive: true, force: true });
  }
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
