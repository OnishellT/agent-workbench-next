import { en } from "./i18n/en";

// Foundry's screens and server messages are written in Spanish. When the host (Agent Workbench)
// or the browser asks for English, this module translates what reaches the DOM from one
// dictionary (i18n/en*.ts): exact strings, plus "{name}" patterns for text built at runtime.
// Anything without an entry stays Spanish, never blank, and the coverage tests name it.
export type Lang = "es" | "en";

let current: Lang = "es";
export const getLang = (): Lang => current;
/** BCP 47 tag for Intl/toLocaleString. */
export const localeTag = () => (current === "en" ? "en" : "es");

const norm = (text: string) => text.replace(/\s+/g, " ").trim();
const exact = new Map<string, string>();
const patterns: { re: RegExp; out: string; weight: number }[] = [];
const escapeRe = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

for (const [key, value] of Object.entries(en)) {
  const core = norm(key);
  if (!/\{\w+\}/.test(core)) {
    exact.set(core, value);
    continue;
  }
  let weight = 0;
  const source = core
    .split(/(\{\w+\})/)
    .map((part) => {
      const name = /^\{(\w+)\}$/.exec(part)?.[1];
      if (name) return `(?<${name}>.+?)`;
      weight += part.length;
      return escapeRe(part);
    })
    .join("");
  patterns.push({ re: new RegExp(`^${source}$`), out: value, weight });
}
// The most specific pattern (most literal text) wins; "1 fuente" beats "{n} fuentes".
patterns.sort((a, b) => b.weight - a.weight);

function direct(core: string): string | undefined {
  const hit = exact.get(core);
  if (hit !== undefined) return hit;
  for (const { re, out } of patterns) {
    const match = re.exec(core);
    if (!match) continue;
    return out.replace(/\{(\w+)\}/g, (_, name: string) => {
      const value = match.groups?.[name] ?? "";
      return lookup(value) ?? value;
    });
  }
  return undefined;
}

// The server joins several sentences into one message (e.g. every readiness blocker); each
// known sentence is translated and the rest is kept, so a partly known text is still readable.
function bySentence(core: string): string | undefined {
  const parts = core.split(/(?<=[.!?])\s+/);
  if (parts.length < 2) return undefined;
  let known = false;
  const out = parts.map((part) => {
    const hit = direct(part);
    if (hit !== undefined) known = true;
    return hit ?? part;
  });
  return known ? out.join(" ") : undefined;
}

function lookup(core: string): string | undefined {
  return direct(core) ?? bySentence(core);
}

/** English for a Spanish UI string (leading/trailing space kept: JSX splits text around
 *  expressions into separate nodes); unchanged when the language is Spanish or unknown. */
export function translate(text: string): string {
  if (current === "es") return text;
  const core = norm(text);
  if (!core) return text;
  const hit = lookup(core);
  if (hit === undefined) return text;
  return /^\s*/.exec(text)![0] + hit + /\s*$/.exec(text)![0];
}

/** Dictionary lookup without the language gate, for tests and the coverage check. */
export const hasTranslation = (text: string) => lookup(norm(text)) !== undefined;

// --- DOM layer -------------------------------------------------------------------------
// Free text the user or the model wrote (transcripts, skill files, code) is never translated.
const SKIP = "pre, code, textarea, script, style, .event-box, .code-block, [data-notranslate]";
const ATTRS = ["placeholder", "title", "aria-label", "alt"];
type Seen = { src: string; out: string };
const texts = new WeakMap<Text, Seen>();
const attrs = new WeakMap<Element, Map<string, Seen>>();

/** `src` is what React wrote; `out` what this module put there. A node whose value is still
 *  `out` was not rewritten by React, so its source is remembered; any other value is new source. */
function apply(value: string, seen: Seen | undefined) {
  const src = seen && value === seen.out ? seen.src : value;
  return { src, out: translate(src) };
}

function syncText(node: Text) {
  const parent = node.parentElement;
  if (!parent || parent.closest(SKIP)) return;
  const value = node.nodeValue ?? "";
  const next = apply(value, texts.get(node));
  texts.set(node, next);
  if (next.out !== value) node.nodeValue = next.out;
}

function syncAttr(element: Element, name: string) {
  if (element.closest(SKIP)) return;
  const value = element.getAttribute(name);
  if (value === null) return;
  const map = attrs.get(element) ?? new Map<string, Seen>();
  attrs.set(element, map);
  const next = apply(value, map.get(name));
  map.set(name, next);
  if (next.out !== value) element.setAttribute(name, next.out);
}

function walk(root: Node) {
  if (root.nodeType === Node.TEXT_NODE) return syncText(root as Text);
  if (root.nodeType !== Node.ELEMENT_NODE) return;
  const element = root as Element;
  const tree = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
  for (let node = tree.nextNode(); node; node = tree.nextNode()) syncText(node as Text);
  const selector = ATTRS.map((name) => `[${name}]`).join(",");
  if (element.matches(selector)) for (const name of ATTRS) syncAttr(element, name);
  for (const child of element.querySelectorAll(selector)) for (const name of ATTRS) syncAttr(child, name);
}

let observer: MutationObserver | null = null;

/** Starts translating the document and keeps doing so as React renders. */
export function startTranslation(lang: Lang) {
  current = lang;
  if (observer) {
    setLang(lang);
    return;
  }
  observer = new MutationObserver((records) => {
    for (const record of records) {
      if (record.type === "characterData") syncText(record.target as Text);
      else if (record.type === "attributes") syncAttr(record.target as Element, record.attributeName!);
      else record.addedNodes.forEach(walk);
    }
  });
  observer.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ATTRS });
  setLang(lang);
}

/** Switches language and rewrites everything already on screen. */
export function setLang(lang: Lang) {
  current = lang;
  if (!observer) return; // no document yet (tests): only the language is recorded
  document.documentElement.lang = lang;
  walk(document.body);
}

/** Language when the host does not say: the browser's, Spanish being Foundry's own. */
export const browserLang = (): Lang => (navigator.language?.toLowerCase().startsWith("es") ? "es" : "en");
export const asLang = (value: unknown): Lang | null => (value === "es" || value === "en" ? value : null);
