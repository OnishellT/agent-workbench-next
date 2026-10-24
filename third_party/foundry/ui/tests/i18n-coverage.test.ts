import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { parse } from "@babel/parser";
import { hasTranslation, setLang } from "../src/i18n";

// Every piece of text the UI can show must have an English entry, so an upstream change that
// adds a Spanish string fails here instead of shipping half-translated. The scan is
// deliberately a superset: JSX text, string attributes, and any string literal or template in
// the sources that is not a CSS class list, an identifier or a path.

const src = fileURLToPath(new URL("../src", import.meta.url));

/** Same in both languages (names, protocol words, code samples): no entry needed. */
const SAME_IN_BOTH = new Set([
  "ID", "Pi", "Codex", "Skill", "Codex CLI", "Foundry", "Conversation Foundry", "v1.0", "GET", "POST",
  "{workspace}", "toast notice",
  '["{python}", "{verifier}/verify.py", "{workspace}"]',
  '["{python}", "{verifier}/introduce_fault.py", "{workspace}"]',
]);
const SKIP_ATTRS = new Set(["className", "key", "type", "variant", "size", "name", "id", "value", "htmlFor", "side", "align", "role", "data-slot", "href", "src", "to"]);

function files(dir: string, out: string[] = []) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      if (name !== "i18n") files(path, out); // the dictionary itself
    } else if (/\.tsx?$/.test(name) && !name.endsWith(".d.ts")) out.push(path);
  }
  return out;
}

const norm = (text: string) => text.replace(/\s+/g, " ").trim();

/** CSS class lists, identifiers, routes and action names are not text. */
function technical(text: string) {
  if (!/\s/.test(text) && /^[a-z0-9_\-:./[\]#@%,()=+*>~'"&?!]+$/.test(text)) return true;
  const tokens = text.split(" ");
  return !/[A-ZÁÉÍÓÚ¿¡·…]/.test(text) && tokens.every((t) => /^[a-z0-9:\-[\]/.()%_=*&>~#!@,]+$/.test(t)) && tokens.some((t) => /[-:[\]]/.test(t));
}

type Node = { type: string; [key: string]: any };

function strings() {
  const found = new Map<string, string>();
  for (const file of files(src)) {
    const ast = parse(readFileSync(file, "utf8"), { sourceType: "module", plugins: ["typescript", "jsx"] });
    // shadcn primitives and the engine hold class lists and regexes: only their JSX text is UI.
    const textOnly = file.includes("/components/ui/") || file.endsWith("/i18n.ts");
    const add = (text: string) => {
      const value = norm(text);
      if (/[A-Za-zÁÉÍÓÚáéíóúñÑ]/.test(value) && !found.has(value)) found.set(value, file.slice(src.length + 1));
    };
    (function visit(node: Node | null, parent: Node | null, key: string) {
      if (!node || typeof node.type !== "string") return;
      if (node.type === "JSXText") add(node.value);
      else if (textOnly) {
        /* class lists and code: not text */
      } else if (node.type === "StringLiteral") {
        const skip =
          parent &&
          (["ImportDeclaration", "ExportNamedDeclaration", "ExportAllDeclaration", "TSLiteralType"].includes(parent.type) ||
            (parent.type === "JSXAttribute" && SKIP_ATTRS.has(parent.name.name)) ||
            (parent.type === "ObjectProperty" && key === "key"));
        if (!skip) add(node.value);
      } else if (node.type === "TemplateLiteral") {
        let text = "";
        node.quasis.forEach((quasi: Node, index: number) => {
          text += quasi.value.cooked;
          if (node.expressions[index]) text += "{x}";
        });
        add(text);
      }
      for (const name of Object.keys(node)) {
        const child = node[name];
        if (Array.isArray(child)) child.forEach((item) => visit(item, node, name));
        else if (child && typeof child.type === "string") visit(child, node, name);
      }
    })(ast.program as Node, null, "");
  }
  return found;
}

test("every UI string has an English entry", () => {
  setLang("en");
  const missing: string[] = [];
  for (const [text, file] of strings()) {
    if (SAME_IN_BOTH.has(text) || technical(text)) continue;
    // "{x}" stands for an interpolated value: probe with a concrete one so patterns match.
    if (!hasTranslation(text.replace(/\{x\}/g, "7"))) missing.push(`${file}: ${text}`);
  }
  assert.deepEqual(missing, [], "strings without an English entry in src/i18n/en.ui.ts");
});
