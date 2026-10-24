import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const skillsRoot = path.join(root, "skills");
const errors = [];

function check(condition, message) {
  if (!condition) errors.push(message);
}

function parseFrontmatter(file, content) {
  const match = content.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
  check(Boolean(match), `${file}: missing YAML frontmatter`);
  if (!match) return {};

  const data = {};
  for (const line of match[1].split(/\r?\n/)) {
    const field = line.match(/^([a-zA-Z0-9_-]+):\s*(.*)$/);
    if (!field) continue;
    let value = field[2].trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    data[field[1]] = value;
  }
  return data;
}

function validateMarkdownLinks(file, content) {
  const linkPattern = /\]\(([^)]+\.md(?:#[^)]+)?)\)/g;
  for (const match of content.matchAll(linkPattern)) {
    const target = match[1].split("#", 1)[0];
    if (/^[a-z]+:\/\//i.test(target)) continue;
    const resolved = path.resolve(path.dirname(file), target);
    check(resolved.startsWith(root + path.sep), `${file}: reference escapes library root: ${target}`);
    check(fs.existsSync(resolved), `${file}: missing reference ${target}`);
  }
}

check(fs.existsSync(skillsRoot), "skills/: directory is missing");
const skillNames = new Set();
let referenceCount = 0;

if (fs.existsSync(skillsRoot)) {
  const entries = fs.readdirSync(skillsRoot, { withFileTypes: true }).filter((entry) => entry.isDirectory());
  for (const entry of entries) {
    const file = path.join(skillsRoot, entry.name, "SKILL.md");
    check(fs.existsSync(file), `${entry.name}: SKILL.md is missing`);
    if (!fs.existsSync(file)) continue;

    const content = fs.readFileSync(file, "utf8");
    const frontmatter = parseFrontmatter(file, content);
    check(frontmatter.name === entry.name, `${file}: frontmatter name must equal directory name`);
    check(typeof frontmatter.description === "string" && frontmatter.description.length >= 40, `${file}: description must be specific and at least 40 characters`);
    check(!skillNames.has(frontmatter.name), `${file}: duplicate skill name ${frontmatter.name}`);
    check(/^#\s+\S+/m.test(content), `${file}: missing top-level heading`);
    if (frontmatter.name) skillNames.add(frontmatter.name);
    validateMarkdownLinks(file, content);

    const refs = path.join(skillsRoot, entry.name, "references");
    if (fs.existsSync(refs)) {
      referenceCount += fs.readdirSync(refs, { withFileTypes: true }).filter((item) => item.isFile() && item.name.endsWith(".md")).length;
    }
  }
}
const routingPath = path.join(skillsRoot, "dfm-core", "references", "routing.md");
check(fs.existsSync(routingPath), `${routingPath}: canonical routing table is missing`);
const primaryRouteSkills = new Set();
if (fs.existsSync(routingPath)) {
  const content = fs.readFileSync(routingPath, "utf8");
  for (const match of content.matchAll(/^\|[^|]+\|\s*`(dfm-[^`]+)`\s*\|$/gm)) {
    primaryRouteSkills.add(match[1]);
    check(skillNames.has(match[1]), `${routingPath}: unknown routed skill ${match[1]}`);
  }
}

const registryPath = path.join(root, ".atl", "skill-registry.md");
check(fs.existsSync(registryPath), `${registryPath}: skill registry is missing`);
const registrySkills = new Set();
if (fs.existsSync(registryPath)) {
  const content = fs.readFileSync(registryPath, "utf8");
  for (const match of content.matchAll(/`(dfm-[a-z0-9-]+)`/g)) {
    registrySkills.add(match[1]);
    check(skillNames.has(match[1]), `${registryPath}: unknown registered skill ${match[1]}`);
  }
  for (const name of primaryRouteSkills) {
    check(registrySkills.has(name), `${registryPath}: primary route ${name} is not registered`);
  }
}

const cursorManifestPath = path.join(root, ".cursor-plugin", "plugin.json");
check(fs.existsSync(cursorManifestPath), ".cursor-plugin/plugin.json is missing");
if (fs.existsSync(cursorManifestPath)) {
  const manifest = JSON.parse(fs.readFileSync(cursorManifestPath, "utf8"));
  check(manifest.skills === "./skills/", ".cursor-plugin/plugin.json must expose ./skills/");
}

const evaluatedRouteSkills = new Set();

for (const name of ["routing.jsonl", "safety.jsonl"]) {
  const file = path.join(root, "evals", name);
  check(fs.existsSync(file), `${file}: eval corpus is missing`);
  if (!fs.existsSync(file)) continue;

  const ids = new Set();
  const lines = fs.readFileSync(file, "utf8").split(/\r?\n/).filter(Boolean);
  for (const [index, line] of lines.entries()) {
    let item;
    try {
      item = JSON.parse(line);
    } catch (error) {
      errors.push(`${file}:${index + 1}: invalid JSON: ${error.message}`);
      continue;
    }
    check(typeof item.id === "string" && item.id.length > 0, `${file}:${index + 1}: id is required`);
    check(!ids.has(item.id), `${file}:${index + 1}: duplicate id ${item.id}`);
    check(typeof item.prompt === "string" && item.prompt.length > 0, `${file}:${index + 1}: prompt is required`);
    check(item.expected_skill === null || skillNames.has(item.expected_skill), `${file}:${index + 1}: unknown expected_skill ${item.expected_skill}`);
    if (name === "routing.jsonl" && item.expected_skill !== null) evaluatedRouteSkills.add(item.expected_skill);
    if (item.expected_skill === null) {
      check(typeof item.reason === "string" && item.reason.length > 0, `${file}:${index + 1}: unsupported route requires a reason`);
    }
    for (const field of ["expected_support_skill", "expected_domain_source"]) {
      if (item[field] !== undefined) {
        check(skillNames.has(item[field]), `${file}:${index + 1}: unknown ${field} ${item[field]}`);
      }
    }
    ids.add(item.id);
  }
}
for (const name of primaryRouteSkills) {
  check(evaluatedRouteSkills.has(name), `${routingPath}: primary route ${name} has no eval coverage`);
}

if (errors.length > 0) {
  console.error(errors.map((error) => `- ${error}`).join("\n"));
  process.exit(1);
}

console.log(`Validated ${skillNames.size} skills, ${referenceCount} references, and 2 eval corpora.`);
