import { readFile } from "node:fs/promises";
import path from "node:path";

const repoRoot = process.cwd();

const schemas = [
  {
    file: "schemas/request-batch.v1.json",
    expectedId: "https://actionrelay.local/schemas/request-batch.v1.json",
    expectedTitle: "ActionRelay Request Batch v1",
    expectedProtocol: "actionrelay.request_batch.v1",
  },
  {
    file: "schemas/result-package.v1.json",
    expectedId: "https://actionrelay.local/schemas/result-package.v1.json",
    expectedTitle: "ActionRelay Result Package v1",
    expectedProtocol: "actionrelay.result_package.v1",
  },
];

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

async function validateSchema(meta) {
  const content = await readFile(path.join(repoRoot, meta.file), "utf8");
  const schema = JSON.parse(content);

  assert(typeof schema === "object" && schema !== null, `${meta.file}: schema must be an object`);
  assert(schema.$schema === "https://json-schema.org/draft/2020-12/schema", `${meta.file}: unexpected $schema`);
  assert(schema.$id === meta.expectedId, `${meta.file}: unexpected $id`);
  assert(schema.title === meta.expectedTitle, `${meta.file}: unexpected title`);
  assert(schema.type === "object", `${meta.file}: root type must be object`);
  assert(schema.additionalProperties === false, `${meta.file}: root additionalProperties must be false`);

  assert(Array.isArray(schema.required), `${meta.file}: required must be an array`);
  assert(schema.required.includes("protocol"), `${meta.file}: required must include protocol`);

  const protocolConst = schema?.properties?.protocol?.const;
  assert(protocolConst === meta.expectedProtocol, `${meta.file}: protocol const mismatch`);
}

async function main() {
  for (const schemaMeta of schemas) {
    await validateSchema(schemaMeta);
  }
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
