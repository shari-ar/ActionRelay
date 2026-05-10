import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";

const REQUEST_BATCH_PROTOCOL = "actionrelay.request_batch.v1";
const RESULT_PACKAGE_PROTOCOL = "actionrelay.result_package.v1";
const DEFAULT_MAX_RESPONSE_BYTES = 65536;
const DEFAULT_REQUEST_TIMEOUT_MS = 8000;
const DEFAULT_WORKER_CONCURRENCY = 4;

async function main() {
  const batchPayload = process.env.ACTIONRELAY_BATCH_B64;
  if (!batchPayload) {
    throw new Error("ACTIONRELAY_BATCH_B64 is required");
  }

  const resultPath = process.env.ACTIONRELAY_RESULT_PATH || "worker/out/result-package.json";
  const decoded = Buffer.from(batchPayload, "base64").toString("utf8");
  const batch = JSON.parse(decoded);

  validateBatch(batch);

  const limits = {
    maxResponseBytesPerRequest: asInt(
      process.env.ACTIONRELAY_MAX_RESPONSE_BYTES,
      batch?.limits?.max_response_bytes_per_request,
      DEFAULT_MAX_RESPONSE_BYTES,
    ),
    requestTimeoutMS: asInt(
      process.env.ACTIONRELAY_REQUEST_TIMEOUT_MS,
      batch?.limits?.request_timeout_ms,
      DEFAULT_REQUEST_TIMEOUT_MS,
    ),
    workerConcurrency: asInt(
      process.env.ACTIONRELAY_WORKER_CONCURRENCY,
      batch?.limits?.worker_concurrency,
      DEFAULT_WORKER_CONCURRENCY,
    ),
  };

  const results = await processRequests(batch.requests, limits);
  const resultPackage = {
    protocol: RESULT_PACKAGE_PROTOCOL,
    batch_id: batch.batch_id,
    ok: results.every((result) => result.ok),
    results,
  };

  await mkdir(path.dirname(resultPath), { recursive: true });
  await writeFile(resultPath, JSON.stringify(resultPackage, null, 2) + "\n", "utf8");
}

function validateBatch(batch) {
  if (!batch || typeof batch !== "object") {
    throw new Error("batch payload must be an object");
  }
  if (batch.protocol !== REQUEST_BATCH_PROTOCOL) {
    throw new Error(`unexpected batch protocol: ${batch.protocol}`);
  }
  if (!batch.batch_id || typeof batch.batch_id !== "string") {
    throw new Error("batch_id is required");
  }
  if (!Array.isArray(batch.requests)) {
    throw new Error("requests must be an array");
  }
}

async function processRequests(requests, limits) {
  const total = requests.length;
  const results = new Array(total);
  let nextIndex = 0;
  const workerCount = Math.max(1, Math.min(limits.workerConcurrency, total || 1));

  await Promise.all(
    Array.from({ length: workerCount }, async () => {
      while (true) {
        const index = nextIndex;
        nextIndex += 1;
        if (index >= total) {
          return;
        }
        const request = requests[index];
        results[index] = await processSingleRequest(request, limits);
      }
    }),
  );

  return results;
}

async function processSingleRequest(request, limits) {
  const requestID = request?.request_id || "unknown";

  try {
    validateRequest(request);

    const method = String(request.method).toUpperCase();
    const headers = new Headers();
    if (request.headers && typeof request.headers === "object") {
      for (const [key, value] of Object.entries(request.headers)) {
        headers.set(String(key).toLowerCase(), String(value));
      }
    }

    let body;
    if (request.body && typeof request.body === "object") {
      if (request.body.encoding !== "base64") {
        return errorResult(requestID, "BODY_TOO_LARGE", "unsupported request body encoding");
      }
      body = Buffer.from(String(request.body.data || ""), "base64");
    }

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), limits.requestTimeoutMS);
    const startedAt = Date.now();

    let response;
    try {
      response = await fetch(request.url, {
        method,
        headers,
        body,
        redirect: "follow",
        signal: controller.signal,
      });
    } finally {
      clearTimeout(timeout);
    }

    const responseHeaders = {};
    for (const [key, value] of response.headers.entries()) {
      responseHeaders[key.toLowerCase()] = value;
    }

    const rawBuffer = Buffer.from(await response.arrayBuffer());
    const truncatedBuffer = rawBuffer.subarray(0, limits.maxResponseBytesPerRequest);
    const truncated = rawBuffer.length > limits.maxResponseBytesPerRequest;
    const contentType = String(response.headers.get("content-type") || "").toLowerCase();

    let encoding = "base64";
    let data = truncatedBuffer.toString("base64");
    if (contentType.includes("json") || contentType.includes("text") || contentType.includes("xml")) {
      encoding = "utf8";
      data = truncatedBuffer.toString("utf8");
    }

    return {
      request_id: requestID,
      ok: true,
      response: {
        status: response.status,
        headers: responseHeaders,
        body: {
          encoding,
          data,
          bytes: rawBuffer.length,
          truncated,
        },
        url: response.url,
        timing_ms: Date.now() - startedAt,
      },
      error: null,
    };
  } catch (error) {
    if (error && error.name === "AbortError") {
      return errorResult(requestID, "TIMEOUT", "request timed out");
    }
    return errorResult(requestID, "WORKER_ERROR", error instanceof Error ? error.message : String(error));
  }
}

function validateRequest(request) {
  if (!request || typeof request !== "object") {
    throw new Error("request must be an object");
  }
  if (!request.request_id || typeof request.request_id !== "string") {
    throw new Error("request_id is required");
  }
  const method = String(request.method || "").toUpperCase();
  const allowedMethods = new Set(["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"]);
  if (!allowedMethods.has(method)) {
    throw new Error(`unsupported method: ${method}`);
  }

  const parsedURL = new URL(String(request.url || ""));
  if (parsedURL.protocol !== "http:" && parsedURL.protocol !== "https:") {
    throw new Error("only http/https is supported");
  }
}

function errorResult(requestID, code, message) {
  return {
    request_id: requestID,
    ok: false,
    response: null,
    error: {
      code,
      message,
    },
  };
}

function asInt(primary, secondary, fallback) {
  const candidates = [primary, secondary, fallback];
  for (const value of candidates) {
    const parsed = Number.parseInt(String(value), 10);
    if (Number.isFinite(parsed) && parsed > 0) {
      return parsed;
    }
  }
  return fallback;
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
