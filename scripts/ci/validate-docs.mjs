import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

const repoRoot = process.cwd();
const docsDir = path.join(repoRoot, "docs");

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function hasBalancedCodeFences(markdown) {
  const lines = markdown.split(/\r?\n/);
  let fenceCount = 0;
  for (const line of lines) {
    if (line.trimStart().startsWith("```")) {
      fenceCount += 1;
    }
  }
  return fenceCount % 2 === 0;
}

async function main() {
  const docsIndexPath = path.join(docsDir, "README.md");
  const docsIndex = await readFile(docsIndexPath, "utf8");

  const docsFiles = (await readdir(docsDir))
    .filter((name) => name.endsWith(".md"))
    .sort();

  for (const name of docsFiles) {
    const filePath = path.join(docsDir, name);
    const markdown = await readFile(filePath, "utf8");

    assert(markdown.trim().length > 0, `docs/${name}: file is empty`);
    assert(markdown.endsWith("\n"), `docs/${name}: file must end with a newline`);
    assert(hasBalancedCodeFences(markdown), `docs/${name}: unbalanced fenced code blocks`);

    if (name !== "README.md") {
      assert(docsIndex.includes(`\`${name}\``), `docs/README.md: missing index entry for ${name}`);
    }
  }

  const rootReadme = await readFile(path.join(repoRoot, "README.md"), "utf8");
  assert(rootReadme.includes("docs/roadmap.md"), "README.md: missing roadmap documentation reference");
}

main().catch((error) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exitCode = 1;
});
