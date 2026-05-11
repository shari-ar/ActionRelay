import { lookup } from "node:dns/promises";
import { mkdir, writeFile } from "node:fs/promises";
import { isIP } from "node:net";
import path from "node:path";

const REQUEST_BATCH_PROTOCOL = "actionrelay.request_batch.v1";
const RESULT_PACKAGE_PROTOCOL = "actionrelay.result_package.v1";
const ROUTE_MODE_WHOLE_DEVICE = "whole_device";
const DEFAULT_MAX_RESPONSE_BYTES = 65536;
const DEFAULT_REQUEST_TIMEOUT_MS = 8000;
const DEFAULT_WORKER_CONCURRENCY = 4;
const MAX_WORKER_CONCURRENCY = 8;
const MAX_REDIRECTS = 5;
const REDACTED_VALUE = "[REDACTED]";

const ALLOWED_METHODS = new Set(["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"]);
const SENSITIVE_REQUEST_HEADERS = new Set(["authorization", "proxy-authorization", "cookie", "x-api-key"]);
const SENSITIVE_RESPONSE_HEADERS = new Set(["set-cookie", "www-authenticate", "proxy-authenticate"]);
const BLOCKED_HOSTS = new Set([
  "localhost",
  "metadata",
  "metadata.google.internal",
  "metadata.google",
  "metadata.azure.internal",
  "metadata.aliyun.internal",
  "instance-data.ec2.internal",
]);
const METADATA_IPS = new Set(["169.254.169.254", "100.100.100.200", "192.0.0.170", "192.0.0.192"]);

class PolicyError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "PolicyError";
    this.code = code;
  }
}

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
  limits.workerConcurrency = clampConcurrency(limits.workerConcurrency);

  const results = await processRequests(batch.requests, limits);
  const resultPackage = {
    protocol: RESULT_PACKAGE_PROTOCOL,
    batch_id: batch.batch_id,
    ok: results.every((result) => result.ok),
    results,
  };

  validateGeneratedResultPackage(resultPackage, batch.requests);

  await mkdir(path.dirname(resultPath), { recursive: true });
  await writeFile(resultPath, JSON.stringify(resultPackage, null, 2) + "\n", "utf8");
}

function validateBatch(batch) {
  if (!batch || typeof batch !== "object" || Array.isArray(batch)) {
    throw new Error("batch payload must be an object");
  }
  assertAllowedKeys(batch, ["protocol", "batch_id", "sent_at", "client", "limits", "requests"], "batch");
  if (batch.protocol !== REQUEST_BATCH_PROTOCOL) {
    throw new Error(`unexpected batch protocol: ${batch.protocol}`);
  }
  if (!isNonEmptyString(batch.batch_id)) {
    throw new Error("batch_id is required");
  }
  if (!isRFC3339(batch.sent_at)) {
    throw new Error("sent_at must be RFC3339");
  }

  const client = batch.client;
  if (!client || typeof client !== "object" || Array.isArray(client)) {
    throw new Error("client metadata is required");
  }
  assertAllowedKeys(client, ["batch_interval_ms", "route_mode"], "client");
  if (!isPositiveInt(client.batch_interval_ms)) {
    throw new Error("client.batch_interval_ms must be > 0");
  }
  if (client.route_mode !== ROUTE_MODE_WHOLE_DEVICE) {
    throw new Error(`client.route_mode must be ${ROUTE_MODE_WHOLE_DEVICE}`);
  }

  const limits = batch.limits;
  if (!limits || typeof limits !== "object" || Array.isArray(limits)) {
    throw new Error("limits are required");
  }
  assertAllowedKeys(
    limits,
    ["max_response_bytes_per_request", "request_timeout_ms", "worker_concurrency"],
    "limits",
  );
  if (!isPositiveInt(limits.max_response_bytes_per_request)) {
    throw new Error("limits.max_response_bytes_per_request must be > 0");
  }
  if (!isPositiveInt(limits.request_timeout_ms)) {
    throw new Error("limits.request_timeout_ms must be > 0");
  }
  if (!isPositiveInt(limits.worker_concurrency)) {
    throw new Error("limits.worker_concurrency must be > 0");
  }

  if (!Array.isArray(batch.requests) || batch.requests.length === 0) {
    throw new Error("requests must be a non-empty array");
  }

  const seenRequestIDs = new Set();
  for (const [index, request] of batch.requests.entries()) {
    validateRequestShape(request, index);
    if (seenRequestIDs.has(request.request_id)) {
      throw new Error(`duplicate request_id in batch: ${request.request_id}`);
    }
    seenRequestIDs.add(request.request_id);
  }
}

function validateRequestShape(request, index) {
  if (!request || typeof request !== "object" || Array.isArray(request)) {
    throw new Error(`requests[${index}] must be an object`);
  }
  assertAllowedKeys(request, ["request_id", "method", "url", "headers", "body"], `requests[${index}]`);
  if (!isNonEmptyString(request.request_id)) {
    throw new Error(`requests[${index}].request_id is required`);
  }
  if (!isNonEmptyString(request.method)) {
    throw new Error(`requests[${index}].method is required`);
  }
  if (!isNonEmptyString(request.url)) {
    throw new Error(`requests[${index}].url is required`);
  }

  if (request.headers !== undefined) {
    if (!request.headers || typeof request.headers !== "object" || Array.isArray(request.headers)) {
      throw new Error(`requests[${index}].headers must be an object when present`);
    }
    for (const [headerName, headerValue] of Object.entries(request.headers)) {
      if (!isNonEmptyString(headerName)) {
        throw new Error(`requests[${index}].headers contains an empty key`);
      }
      if (typeof headerValue !== "string") {
        throw new Error(`requests[${index}].headers values must be strings`);
      }
    }
  }

  if (request.body === null || request.body === undefined) {
    return;
  }
  if (typeof request.body !== "object" || Array.isArray(request.body)) {
    throw new Error(`requests[${index}].body must be null or an object`);
  }
  assertAllowedKeys(request.body, ["encoding", "data"], `requests[${index}].body`);
  if (request.body.encoding !== "base64") {
    throw new Error(`requests[${index}].body.encoding must be base64`);
  }
  if (typeof request.body.data !== "string") {
    throw new Error(`requests[${index}].body.data must be a string`);
  }
}

function validateGeneratedResultPackage(resultPackage, requests) {
  if (!resultPackage || typeof resultPackage !== "object" || Array.isArray(resultPackage)) {
    throw new Error("generated result package must be an object");
  }
  if (resultPackage.protocol !== RESULT_PACKAGE_PROTOCOL) {
    throw new Error(`generated result protocol mismatch: ${resultPackage.protocol}`);
  }
  if (!isNonEmptyString(resultPackage.batch_id)) {
    throw new Error("generated result batch_id is required");
  }
  if (!Array.isArray(resultPackage.results)) {
    throw new Error("generated result package results must be an array");
  }

  const expectedRequestIDs = new Set(requests.map((request) => String(request.request_id)));
  const seenResultIDs = new Set();

  for (const [index, result] of resultPackage.results.entries()) {
    if (!result || typeof result !== "object" || Array.isArray(result)) {
      throw new Error(`results[${index}] must be an object`);
    }
    if (!isNonEmptyString(result.request_id)) {
      throw new Error(`results[${index}].request_id is required`);
    }
    if (seenResultIDs.has(result.request_id)) {
      throw new Error(`duplicate result request_id: ${result.request_id}`);
    }
    seenResultIDs.add(result.request_id);
    if (!expectedRequestIDs.has(result.request_id)) {
      throw new Error(`unexpected result request_id: ${result.request_id}`);
    }
    if (result.ok === true) {
      if (!result.response || result.error !== null) {
        throw new Error(`results[${index}] must include response and null error when ok=true`);
      }
      if (!isNonEmptyString(result.response.url)) {
        throw new Error(`results[${index}].response.url is required`);
      }
      if (!Number.isInteger(result.response.timing_ms) || result.response.timing_ms < 0) {
        throw new Error(`results[${index}].response.timing_ms must be >= 0`);
      }
      if (!result.response.body || typeof result.response.body !== "object") {
        throw new Error(`results[${index}].response.body is required`);
      }
      if (!Number.isInteger(result.response.body.bytes) || result.response.body.bytes < 0) {
        throw new Error(`results[${index}].response.body.bytes must be >= 0`);
      }
      if (typeof result.response.body.data !== "string") {
        throw new Error(`results[${index}].response.body.data must be a string`);
      }
      if (typeof result.response.body.encoding !== "string") {
        throw new Error(`results[${index}].response.body.encoding must be a string`);
      }
      if (typeof result.response.body.truncated !== "boolean") {
        throw new Error(`results[${index}].response.body.truncated must be a boolean`);
      }
    } else {
      if (result.response !== null || !result.error) {
        throw new Error(`results[${index}] must include null response and error when ok=false`);
      }
      if (!isNonEmptyString(result.error.code)) {
        throw new Error(`results[${index}].error.code is required`);
      }
      if (typeof result.error.message !== "string") {
        throw new Error(`results[${index}].error.message must be a string`);
      }
    }
  }

  for (const requestID of expectedRequestIDs) {
    if (!seenResultIDs.has(requestID)) {
      throw new Error(`missing result for request_id: ${requestID}`);
    }
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
    const normalized = normalizeRequest(request);
    const startedAt = Date.now();

    const response = await fetchWithGuardrails(normalized, limits);
    const responseHeaders = redactResponseHeaders(response.headers);

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
        url: sanitizeOutputURL(response.url),
        timing_ms: Date.now() - startedAt,
      },
      error: null,
    };
  } catch (error) {
    if (error && error.name === "AbortError") {
      return errorResult(requestID, "TIMEOUT", "request timed out");
    }
    if (error instanceof PolicyError) {
      return errorResult(requestID, error.code, error.message);
    }
    return errorResult(requestID, "WORKER_ERROR", error instanceof Error ? error.message : String(error));
  }
}

function normalizeRequest(request) {
  validateRequestShape(request, 0);

  const requestID = String(request.request_id);
  const method = String(request.method).toUpperCase();
  if (!ALLOWED_METHODS.has(method)) {
    throw new PolicyError("METHOD_REJECTED", `unsupported method: ${method}`);
  }

  let parsedURL;
  try {
    parsedURL = new URL(String(request.url));
  } catch {
    throw new PolicyError("URL_REJECTED", "url must be a valid absolute URL");
  }

  const requestHeaders = new Headers();
  if (request.headers && typeof request.headers === "object") {
    for (const [key, value] of Object.entries(request.headers)) {
      requestHeaders.set(String(key).toLowerCase(), String(value));
    }
  }

  let requestBody;
  if (request.body && typeof request.body === "object") {
    if (request.body.encoding !== "base64") {
      throw new PolicyError("BODY_TOO_LARGE", "unsupported request body encoding");
    }
    try {
      requestBody = Buffer.from(String(request.body.data || ""), "base64");
    } catch {
      throw new PolicyError("BODY_TOO_LARGE", "invalid base64 request body");
    }
  }

  return {
    requestID,
    method,
    url: parsedURL,
    headers: requestHeaders,
    body: requestBody,
  };
}

async function fetchWithGuardrails(request, limits) {
  const deadline = Date.now() + limits.requestTimeoutMS;

  let currentURL = new URL(request.url.toString());
  let currentMethod = request.method;
  let currentBody = request.body;
  let currentHeaders = new Headers(request.headers);

  for (let redirectCount = 0; ; redirectCount += 1) {
    await enforceURLGuardrails(currentURL);

    const remaining = deadline - Date.now();
    if (remaining <= 0) {
      throw new PolicyError("TIMEOUT", "request timed out");
    }

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), remaining);

    let response;
    try {
      response = await fetch(currentURL, {
        method: currentMethod,
        headers: currentHeaders,
        body: allowsBody(currentMethod) ? currentBody : undefined,
        redirect: "manual",
        signal: controller.signal,
      });
    } finally {
      clearTimeout(timeout);
    }

    if (!isRedirectStatus(response.status)) {
      return response;
    }

    if (redirectCount >= MAX_REDIRECTS) {
      throw new PolicyError("URL_REJECTED", `too many redirects (>${MAX_REDIRECTS})`);
    }

    const location = response.headers.get("location");
    if (!location) {
      throw new PolicyError("URL_REJECTED", "redirect response missing location header");
    }

    let nextURL;
    try {
      nextURL = new URL(location, currentURL);
    } catch {
      throw new PolicyError("URL_REJECTED", "redirect location is invalid");
    }

    await enforceURLGuardrails(nextURL);

    const redirectAdjusted = adjustRedirectMethodAndBody(currentMethod, currentBody, response.status, currentHeaders);
    if (response.body) {
      try {
        await response.body.cancel();
      } catch {
      }
    }
    currentMethod = redirectAdjusted.method;
    currentBody = redirectAdjusted.body;
    currentHeaders = dropSensitiveHeadersOnCrossOriginRedirect(redirectAdjusted.headers, currentURL, nextURL);
    currentURL = nextURL;
  }
}

function adjustRedirectMethodAndBody(method, body, status, headers) {
  const nextHeaders = new Headers(headers);
  if (status === 303 || ((status === 301 || status === 302) && method === "POST")) {
    nextHeaders.delete("content-length");
    nextHeaders.delete("content-type");
    return { method: "GET", body: undefined, headers: nextHeaders };
  }
  return { method, body, headers: nextHeaders };
}

function dropSensitiveHeadersOnCrossOriginRedirect(headers, fromURL, toURL) {
  if (fromURL.origin === toURL.origin) {
    return headers;
  }
  const next = new Headers(headers);
  for (const headerName of SENSITIVE_REQUEST_HEADERS) {
    next.delete(headerName);
  }
  return next;
}

async function enforceURLGuardrails(targetURL) {
  if (targetURL.protocol !== "http:" && targetURL.protocol !== "https:") {
    throw new PolicyError("URL_REJECTED", "only http and https URLs are allowed");
  }
  if (targetURL.username || targetURL.password) {
    throw new PolicyError("URL_REJECTED", "url credentials are not allowed");
  }

  const host = String(targetURL.hostname || "").toLowerCase();
  if (!host) {
    throw new PolicyError("URL_REJECTED", "destination host is missing");
  }

  if (BLOCKED_HOSTS.has(host) || host.endsWith(".localhost")) {
    throw new PolicyError("REQUEST_BLOCKED", "destination host is blocked");
  }

  if (isIP(host)) {
    enforceIPAddressPolicy(host);
    return;
  }

  let resolved;
  try {
    resolved = await lookup(host, { all: true, verbatim: true });
  } catch {
    throw new PolicyError("URL_REJECTED", "failed to resolve destination host");
  }

  if (!Array.isArray(resolved) || resolved.length === 0) {
    throw new PolicyError("URL_REJECTED", "destination host did not resolve to an address");
  }

  for (const item of resolved) {
    enforceIPAddressPolicy(item.address);
  }
}

function enforceIPAddressPolicy(ip) {
  if (!isIP(ip)) {
    throw new PolicyError("URL_REJECTED", "destination address is invalid");
  }
  if (METADATA_IPS.has(ip)) {
    throw new PolicyError("REQUEST_BLOCKED", "metadata service destinations are blocked");
  }
  if (isIP(ip) === 4) {
    if (isBlockedIPv4(ip)) {
      throw new PolicyError("REQUEST_BLOCKED", "private, loopback, link-local, or reserved IPv4 destinations are blocked");
    }
    return;
  }
  if (isBlockedIPv6(ip)) {
    throw new PolicyError("REQUEST_BLOCKED", "private, loopback, link-local, or reserved IPv6 destinations are blocked");
  }
}

function isBlockedIPv4(ip) {
  const octets = ip.split(".").map((part) => Number.parseInt(part, 10));
  if (octets.length !== 4 || octets.some((value) => Number.isNaN(value) || value < 0 || value > 255)) {
    return true;
  }

  const [a, b] = octets;

  if (a === 0 || a === 10 || a === 127) {
    return true;
  }
  if (a === 169 && b === 254) {
    return true;
  }
  if (a === 172 && b >= 16 && b <= 31) {
    return true;
  }
  if (a === 192 && b === 168) {
    return true;
  }
  if (a === 100 && b >= 64 && b <= 127) {
    return true;
  }
  if (a >= 224) {
    return true;
  }
  return false;
}

function isBlockedIPv6(ip) {
  const normalized = ip.toLowerCase();

  if (normalized === "::" || normalized === "::1") {
    return true;
  }
  if (normalized.startsWith("fc") || normalized.startsWith("fd")) {
    return true;
  }
  if (/^fe[89ab]/.test(normalized)) {
    return true;
  }
  if (normalized.startsWith("ff")) {
    return true;
  }
  return false;
}

function redactResponseHeaders(headers) {
  const responseHeaders = {};
  for (const [key, value] of headers.entries()) {
    const lower = key.toLowerCase();
    if (SENSITIVE_RESPONSE_HEADERS.has(lower)) {
      responseHeaders[lower] = REDACTED_VALUE;
      continue;
    }
    responseHeaders[lower] = value;
  }
  return responseHeaders;
}

function sanitizeOutputURL(rawURL) {
  try {
    const parsed = new URL(rawURL);
    parsed.username = "";
    parsed.password = "";
    return parsed.toString();
  } catch {
    return rawURL;
  }
}

function errorResult(requestID, code, message) {
  return {
    request_id: requestID,
    ok: false,
    response: null,
    error: {
      code,
      message: redactSecretText(message),
    },
  };
}

function redactSecretText(message) {
  const raw = typeof message === "string" ? message : String(message ?? "");
  return raw
    .replace(
      /(authorization|proxy-authorization|cookie|set-cookie|x-api-key)\s*[:=]\s*[^,\s;]+/gi,
      (_match, key) => `${key}: ${REDACTED_VALUE}`,
    )
    .replace(
      /(token|access_token|id_token|client_secret|password)=([^&\s]+)/gi,
      (_match, key) => `${key}=${REDACTED_VALUE}`,
    );
}

function isRedirectStatus(statusCode) {
  return statusCode === 301 || statusCode === 302 || statusCode === 303 || statusCode === 307 || statusCode === 308;
}

function allowsBody(method) {
  return method !== "GET" && method !== "HEAD";
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

function clampConcurrency(value) {
  return Math.max(1, Math.min(MAX_WORKER_CONCURRENCY, value));
}

function isNonEmptyString(value) {
  return typeof value === "string" && value.trim() !== "";
}

function isPositiveInt(value) {
  return Number.isInteger(value) && value > 0;
}

function isRFC3339(value) {
  if (typeof value !== "string") {
    return false;
  }
  const parsed = Date.parse(value);
  return Number.isFinite(parsed);
}

function assertAllowedKeys(objectValue, allowedKeys, fieldName) {
  const allowed = new Set(allowedKeys);
  for (const key of Object.keys(objectValue)) {
    if (!allowed.has(key)) {
      throw new Error(`${fieldName} contains unsupported field: ${key}`);
    }
  }
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(redactSecretText(message));
  process.exitCode = 1;
});
