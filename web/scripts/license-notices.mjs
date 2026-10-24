// Compone /NOTICES.txt (NOTICE.md + módulos Go + dependencias web); sin licencia, falla.
import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const project = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
const seen = new Map();

function collect(name, from) {
  const require = createRequire(path.join(from, "package.json"));
  let manifest;
  for (const base of require.resolve.paths(name) || []) {
    const candidate = path.join(base, name, "package.json");
    if (existsSync(candidate)) {
      manifest = candidate;
      break;
    }
  }
  if (!manifest) throw new Error(`Installed package missing: ${name}`);
  const dir = path.dirname(manifest);
  const pkg = JSON.parse(readFileSync(manifest, "utf8"));
  const key = `${pkg.name}@${pkg.version}`;
  if (seen.has(key)) return;
  const files = readdirSync(dir).filter((file) => /^(license|licence|copying|notice)(\.|$)/i.test(file));
  if (!files.length) throw new Error(`No license file: ${key}`);
  seen.set(key, {
    pkg,
    texts: files.sort().map((file) => `${file}\n${readFileSync(path.join(dir, file), "utf8")}`),
  });
  for (const dep of Object.keys(pkg.dependencies || {})) collect(dep, dir);
}

// tailwindcss and tw-animate-css generate the stylesheet the release serves.
for (const name of [...Object.keys(project.dependencies), "tailwindcss", "tw-animate-css"]) {
  collect(name, root);
}

const text =
  [
    "Agent Workbench: versión, avisos, licencias y fuente correspondiente",
    readFileSync(path.join(root, "../NOTICE.md"), "utf8").trim(),
    readFileSync(path.join(root, "../GO-NOTICES.txt"), "utf8").trim(),
    "=".repeat(72),
    "Avisos de las dependencias del paquete web que este release distribuye",
    `Generado por web/scripts/license-notices.mjs desde la instalación exacta de web/bun.lock (${seen.size} paquetes). Los textos son los que publica cada paquete instalado.`,
    ...[...seen]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(
        ([name, { pkg, texts }]) =>
          `\n${"=".repeat(72)}\n${name}\nLicense: ${pkg.license || "See notice"}\n\n${texts.join("\n\n")}`,
      ),
  ].join("\n") + "\n";

writeFileSync(path.join(root, "public", "NOTICES.txt"), text);
console.log(`Recorded ${seen.size} runtime package licenses`);
