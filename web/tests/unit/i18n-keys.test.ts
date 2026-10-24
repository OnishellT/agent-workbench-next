import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

/** DX guard: a dictionary entry nobody references is dead text that keeps being
 *  translated and reviewed. Every key of every `i18n.ts` under web/src must appear as a
 *  string literal in another source file, or in the Go backend when the server is the one
 *  that returns that text (the UI only translates it). */
const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const srcRoot = join(webRoot, "src");
const goRoots = [join(webRoot, "..", "internal"), join(webRoot, "..", "cmd")];

function walk(dir: string, match: RegExp, out: string[] = []) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) walk(path, match, out);
    else if (match.test(name)) out.push(path);
  }
  return out;
}

/** Keys of the `english` literal: `"quoted text": ...` or `bareWord: ...` at entry level. */
function dictionaryKeys(source: string): string[] {
  const start = source.search(/const (?:english|\w+English)\b[^=]*=\s*\{/);
  if (start < 0) return [];
  const body = source.slice(source.indexOf("{", start));
  const end = body.search(/^\};/m);
  const keys: string[] = [];
  const entry = /^ {2}(?:("(?:[^"\\]|\\.)*")|([\p{L}_][\p{L}\p{N}_]*))\s*:/gmu;
  for (const match of body.slice(0, end < 0 ? undefined : end).matchAll(entry))
    keys.push(match[1] ? (JSON.parse(match[1]) as string) : match[2]);
  return keys;
}

const dictionaries = walk(srcRoot, /^i18n\.ts$/);
const sources = walk(srcRoot, /\.tsx?$/).filter((path) => !dictionaries.includes(path));
const sourceText = sources.map((path) => readFileSync(path, "utf8")).join("\n");
const goText = goRoots
  .flatMap((root) => walk(root, /\.go$/))
  .map((path) => readFileSync(path, "utf8"))
  .join("\n");

function referenced(key: string) {
  const literal = JSON.stringify(key).slice(1, -1);
  for (const quote of ['"', "'", "`"])
    if (sourceText.includes(quote + literal + quote)) return true;
  return goText.includes(key);
}

test("there are dictionaries to guard", () => {
  assert.ok(dictionaries.length >= 4, "i18n.ts files not found");
});

for (const path of dictionaries) {
  test(`every key of ${relative(srcRoot, path)} is referenced`, () => {
    const keys = dictionaryKeys(readFileSync(path, "utf8"));
    assert.ok(keys.length > 0, "no keys parsed");
    const dead = keys.filter((key) => !referenced(key));
    assert.deepEqual(dead, [], "dictionary keys no source file references");
  });
}
