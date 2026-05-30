import { readFile } from "node:fs/promises";
import path from "node:path";

const repoRoot = process.cwd();

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

async function loadJSON(filePath) {
  const content = await readFile(filePath, "utf8");
  return JSON.parse(content);
}

async function validateGoModuleVersion() {
  const goModPath = path.join(repoRoot, "client", "go.mod");
  const goMod = await readFile(goModPath, "utf8");
  const match = goMod.match(/^go\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)/m);
  assert(match, "client/go.mod: missing go directive");
  return match[1];
}

async function validateCIWorkflow(expectedGoVersion) {
  const ciPath = path.join(repoRoot, ".github", "workflows", "ci.yml");
  const ci = await readFile(ciPath, "utf8");

  assert(ci.includes("go-version-file: client/go.mod"), "ci.yml: must use go-version-file from client/go.mod");
  assert(ci.includes("go test ./..."), "ci.yml: missing go test step");
  assert(ci.includes("gofmt -l ."), "ci.yml: missing gofmt verification");
  assert(ci.includes("Validate JSON schemas"), "ci.yml: missing schema validation step");
  assert(ci.includes("Validate documentation"), "ci.yml: missing docs validation step");
  assert(expectedGoVersion.length > 0, "go version must be non-empty");
}

async function validateReleaseWorkflow() {
  const releasePath = path.join(repoRoot, ".github", "workflows", "release.yml");
  const release = await readFile(releasePath, "utf8");

  assert(release.includes("release:\n    types:\n      - published"), "release.yml: must trigger on release published");
  assert(release.includes("workflow_dispatch:"), "release.yml: missing workflow_dispatch recovery trigger");
  assert(release.includes("actions/setup-go"), "release.yml: missing setup-go");
  assert(release.includes("go-version-file: client/go.mod"), "release.yml: go version must come from client/go.mod");
  assert(release.includes("Build cross-platform binaries and checksums"), "release.yml: missing build assets step");
  assert(release.includes("gh release upload"), "release.yml: missing release asset upload");
  assert(release.includes("dist/*.tar.gz"), "release.yml: missing tar.gz artifacts");
  assert(release.includes("dist/SHA256SUMS.txt"), "release.yml: missing SHA256SUMS artifact");
}

async function validateProtocolInvariants() {
  const requestSchema = await loadJSON(path.join(repoRoot, "schemas", "request-batch.v1.json"));
  const resultSchema = await loadJSON(path.join(repoRoot, "schemas", "result-package.v1.json"));

  assert(
    requestSchema?.properties?.client?.properties?.route_mode?.const === "whole_device",
    "request-batch schema: client.route_mode must remain whole_device",
  );
  assert(
    requestSchema?.properties?.protocol?.const === "actionrelay.request_batch.v1",
    "request-batch schema: protocol const mismatch",
  );
  assert(
    resultSchema?.properties?.protocol?.const === "actionrelay.result_package.v1",
    "result-package schema: protocol const mismatch",
  );

  const protocolTypes = await readFile(path.join(repoRoot, "client", "internal", "protocol", "types.go"), "utf8");
  assert(
    protocolTypes.includes('RequestBatchProtocol  = "actionrelay.request_batch.v1"'),
    "client/internal/protocol/types.go: request batch protocol constant mismatch",
  );
  assert(
    protocolTypes.includes('ResultPackageProtocol = "actionrelay.result_package.v1"'),
    "client/internal/protocol/types.go: result package protocol constant mismatch",
  );
}

async function validateConfigContract() {
  const configSource = await readFile(path.join(repoRoot, "client", "internal", "config", "config.go"), "utf8");
  assert(configSource.includes("ConfigVersion"), "config.go: missing ConfigVersion field");
  assert(configSource.includes("ConfigVersion:          1"), "config.go: default config version must be 1");
  assert(configSource.includes("config_version must be 1"), "config.go: must enforce config version contract");
}

async function validateDocsCoverage() {
  const usage = await readFile(path.join(repoRoot, "docs", "usage.md"), "utf8");
  const release = await readFile(path.join(repoRoot, "docs", "release.md"), "utf8");
  const limitations = await readFile(path.join(repoRoot, "docs", "limitations.md"), "utf8");
  const docsIndex = await readFile(path.join(repoRoot, "docs", "README.md"), "utf8");

  assert(usage.includes("actionrelay status"), "docs/usage.md: missing status usage guidance");
  assert(usage.includes("diagnostics"), "docs/usage.md: missing diagnostics guidance");
  assert(release.includes("Release Flow"), "docs/release.md: missing release flow section");
  assert(release.includes("CI Coverage"), "docs/release.md: missing CI coverage section");
  assert(docsIndex.includes("`limitations.md`"), "docs/README.md: missing limitations index entry");
  assert(limitations.includes("CONNECT tunneling is not supported"), "docs/limitations.md: missing CONNECT limitation");
  assert(limitations.includes("GitHub Actions only"), "docs/limitations.md: missing GitHub Actions invariant");
  assert(limitations.includes("GitHub-domain constrained"), "docs/limitations.md: missing GitHub-domain invariant");
}

async function main() {
  const goVersion = await validateGoModuleVersion();
  await validateCIWorkflow(goVersion);
  await validateReleaseWorkflow();
  await validateProtocolInvariants();
  await validateConfigContract();
  await validateDocsCoverage();
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
