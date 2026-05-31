import { readFile } from "node:fs/promises";
import path from "node:path";

const repoRoot = process.cwd();

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

async function main() {
  const docsIndex = await readFile(path.join(repoRoot, "docs", "README.md"), "utf8");
  const finalValidation = await readFile(path.join(repoRoot, "docs", "final-validation.md"), "utf8");
  const releaseDoc = await readFile(path.join(repoRoot, "docs", "release.md"), "utf8");
  const compatibilityDoc = await readFile(path.join(repoRoot, "docs", "compatibility.md"), "utf8");
  const limitationsDoc = await readFile(path.join(repoRoot, "docs", "limitations.md"), "utf8");
  const releaseWorkflow = await readFile(path.join(repoRoot, ".github", "workflows", "release.yml"), "utf8");

  assert(docsIndex.includes("`final-validation.md`"), "docs/README.md: missing final-validation index entry");
  assert(finalValidation.includes("Windows"), "docs/final-validation.md: missing Windows validation requirement");
  assert(finalValidation.includes("macOS"), "docs/final-validation.md: missing macOS validation requirement");
  assert(finalValidation.includes("Linux"), "docs/final-validation.md: missing Linux validation requirement");
  assert(finalValidation.includes("GitHub Actions-only"), "docs/final-validation.md: missing GitHub Actions-only invariant check");
  assert(finalValidation.includes("GitHub-domain-only"), "docs/final-validation.md: missing GitHub-domain-only invariant check");
  assert(finalValidation.includes("supportability.health_class=healthy"), "docs/final-validation.md: missing supportability health check");
  assert(finalValidation.includes("supportability.needs_attention=false"), "docs/final-validation.md: missing supportability attention check");
  assert(finalValidation.includes("Release Asset Validation Checklist"), "docs/final-validation.md: missing release asset validation section");
  assert(finalValidation.includes("SHA256SUMS.txt"), "docs/final-validation.md: missing checksum validation requirement");
  assert(finalValidation.includes("RELEASE_MANIFEST.txt"), "docs/final-validation.md: missing release manifest validation requirement");
  assert(finalValidation.includes("sha256sum -c SHA256SUMS.txt"), "docs/final-validation.md: missing checksum verification command");
  assert(finalValidation.includes("Release workflow has produced and uploaded the full asset set"), "docs/final-validation.md: missing release asset exit criterion");
  assert(releaseDoc.includes("final-validation.md"), "docs/release.md: missing reference to final-validation.md");
  assert(compatibilityDoc.includes("GitHub Actions"), "docs/compatibility.md: missing GitHub Actions invariant");
  assert(limitationsDoc.includes("GitHub-domain"), "docs/limitations.md: missing GitHub-domain limitation text");
  assert(releaseWorkflow.includes("release:\n    types:\n      - published"), "release.yml: must trigger on published releases");
  assert(releaseWorkflow.includes("gh release upload"), "release.yml: missing release upload step");
  assert(releaseWorkflow.includes("dist/SHA256SUMS.txt"), "release.yml: missing checksum upload");
  assert(releaseWorkflow.includes("dist/RELEASE_MANIFEST.txt"), "release.yml: missing manifest upload");
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
