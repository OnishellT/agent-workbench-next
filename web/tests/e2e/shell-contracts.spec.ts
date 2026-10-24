import { expect, test } from "@playwright/test";
import { matchCommands, type CommandEntry } from "../../src/app/commands";
import {
  MAX_CONNECTIONS,
  connectionName,
  connectionOrigin,
  exportConnections,
  importConnections,
  mergeConnection,
  readConnections,
  type ConnectionDTO,
} from "../../src/app/connections-store";
import { shellDictionary, translateShell } from "../../src/app/i18n";
import { readRoute, sections } from "../../src/app/navigation";
import { readFile } from "node:fs/promises";
import { views as modelsViews } from "../../src/modules/models/domain";
import { conversationViews, tasksViews } from "../../src/modules/conversations/domain";
import { foundryViews } from "../../src/modules/foundry/domain";

// Pure contracts of the shell slice (UI-02/UI-04/UI-05/UI-06 + the shared light
// theme): no browser, no backend, no mock. They run with the Playwright runner:
//   bun run test tests/e2e/shell-contracts.spec.ts

test("a connection is an origin: no credentials, path, query or remote HTTP", () => {
  expect(connectionOrigin("https://workbench.example.com")).toBe(
    "https://workbench.example.com",
  );
  expect(connectionOrigin("  https://workbench.example.com/  ")).toBe(
    "https://workbench.example.com",
  );
  expect(connectionOrigin("http://127.0.0.1:18888")).toBe("http://127.0.0.1:18888");
  expect(connectionOrigin("http://localhost:18888/")).toBe("http://localhost:18888");

  for (const value of [
    "http://workbench.example.com",
    "https://user:pass@workbench.example.com",
    "https://workbench.example.com/?token=abc",
    "https://workbench.example.com/#token",
    "https://workbench.example.com/api",
    "ftp://workbench.example.com",
    "workbench.example.com",
    "",
  ])
    expect(() => connectionOrigin(value), value).toThrow();
});

test("names are trimmed, bounded and control-free", () => {
  expect(connectionName("  Mi VPS  ")).toBe("Mi VPS");
  expect(connectionName("x".repeat(64))).toBe("x".repeat(64));
  for (const value of ["", "   ", "x".repeat(65), "mal\u0000nombre", "salto\ndenlínea"])
    expect(() => connectionName(value), JSON.stringify(value)).toThrow();
});

test("stored connections drop what the browser can no longer justify", () => {
  const stored = JSON.stringify([
    { name: "Válida", origin: "https://workbench.example.com" },
    { name: "Repetida", origin: "https://workbench.example.com" },
    { name: "HTTP remoto", origin: "http://workbench.example.com" },
    { name: "Con token", origin: "https://workbench.example.com/?token=x" },
    { name: 7, origin: "https://workbench.example.com" },
  ]);
  expect(readConnections(stored)).toEqual([
    { name: "Válida", origin: "https://workbench.example.com" },
  ]);
  expect(readConnections(null)).toEqual([]);
  expect(() => readConnections("{}")).toThrow(); // not an array: nothing is invented
  expect(() => readConnections('{"a":')).toThrow();
  expect(() => readConnections("x".repeat(65537))).toThrow();
});

test("merging keeps one record per origin and stops at the limit", () => {
  const one = mergeConnection([], { name: "A", origin: "https://a.example" });
  expect(one).toEqual([{ name: "A", origin: "https://a.example" }]);
  expect(
    mergeConnection(one, { name: "A2", origin: "https://a.example" }),
  ).toEqual([{ name: "A2", origin: "https://a.example" }]);

  const full: ConnectionDTO[] = Array.from({ length: MAX_CONNECTIONS }, (_, i) => ({
    name: `vps-${i}`,
    origin: `https://vps-${i}.example`,
  }));
  expect(() =>
    mergeConnection(full, { name: "extra", origin: "https://extra.example" }),
  ).toThrow();
  expect(mergeConnection(full, { name: "vps-0", origin: "https://vps-0.example" })).toHaveLength(
    MAX_CONNECTIONS,
  );
});

test("export and import exchange names and addresses only", () => {
  const connections = [
    { name: "VPS", origin: "https://workbench.example.com" },
    { name: "Local", origin: "http://127.0.0.1:18888" },
  ];
  const text = exportConnections(connections);
  const parsed = JSON.parse(text);
  expect(Object.keys(parsed)).toEqual(["version", "connections"]);
  expect(parsed.version).toBe(1);
  for (const record of parsed.connections)
    expect(Object.keys(record).sort()).toEqual(["name", "origin"]);
  expect(text).not.toMatch(/token|ssh|project|account|password|cookie/i);
  expect(importConnections([], text)).toEqual(connections);
});

test("a bad file leaves the current connections untouched", () => {
  const current = [{ name: "VPS", origin: "https://workbench.example.com" }];
  const cases: [string, string][] = [
    ["json inválido", "{no-json"],
    ["versión ajena", '{"version":2,"connections":[]}'],
    ["propiedad extra", '{"version":1,"connections":[],"accounts":[]}'],
    [
      "registro con secreto",
      '{"version":1,"connections":[{"name":"a","origin":"https://a.example","token":"x"}]}',
    ],
    ["origen http remoto", '{"version":1,"connections":[{"name":"a","origin":"http://a.example"}]}'],
    [
      "más de 24",
      JSON.stringify({
        version: 1,
        connections: Array.from({ length: MAX_CONNECTIONS + 1 }, (_, i) => ({
          name: `v${i}`,
          origin: `https://v${i}.example`,
        })),
      }),
    ],
  ];
  for (const [label, text] of cases) {
    const before = JSON.stringify(current);
    expect(() => importConnections(current, text), label).toThrow();
    expect(JSON.stringify(current), label).toBe(before);
  }
  expect(() => importConnections(current, " ".repeat(65537))).toThrow();
  expect(
    importConnections(current, exportConnections([{ name: "Nueva", origin: "https://new.example" }])),
  ).toEqual([...current, { name: "Nueva", origin: "https://new.example" }]);
});

test("palette search ignores accents, case and ranking order is stable", () => {
  const entries: CommandEntry[] = [
    { id: "view:models/catalog", group: "Modelos", label: "Catálogo" },
    { id: "section:settings", group: "Instalación", label: "Ajustes", keywords: "proyectos ejecución" },
    { id: "action:theme", group: "Preferencias rápidas", label: "Cambiar de tema", keywords: "tema" },
  ];
  expect(matchCommands(entries, "catalogo").map((e) => e.id)).toEqual([
    "view:models/catalog",
  ]);
  expect(matchCommands(entries, "CATÁLOGO").map((e) => e.id)).toEqual([
    "view:models/catalog",
  ]);
  expect(matchCommands(entries, "proyectos").map((e) => e.id)).toEqual([
    "section:settings",
  ]);
  expect(matchCommands(entries, "  ").map((e) => e.id)).toEqual(entries.map((e) => e.id));
  expect(matchCommands(entries, "zzz")).toEqual([]);
  expect(matchCommands(entries, "a", 2)).toHaveLength(2);
});

test("Atlas is retired and its old deep links resolve inside the same router", () => {
  expect(sections.some((section) => section.id === "atlas")).toBe(false);
  const hosts = sections.find((section) => section.id === "connections");
  expect(hosts?.label).toBe("Conexiones");
  expect(sections.map(s => s.id)).toEqual(["conversations", "tasks", "foundry", "sim", "models", "connections", "settings"]);
  // The host registry kept its screen: an old bookmark lands there instead of a dead id.
  expect(readRoute("http://localhost/?section=atlas&view=hosts").section).toBe(hosts);
  // Consumption is the selected host's Magpie under Modelos → Uso, not a second dashboard.
  expect(readRoute("http://localhost/?section=atlas&view=pricing")).toEqual({
    section: sections.find((section) => section.id === "models"),
    view: "usage",
  });
  expect(readRoute("http://localhost/?section=atlas").view).toBe("usage");
  // The old filter query is ignored, never a second authority over the route.
  expect(
    readRoute("http://localhost/?section=atlas&view=activity&period=30&dataset=demo")
      .view,
  ).toBe("usage");
  expect(readRoute("http://localhost/?section=hosts").section).toBe(hosts);
  expect(readRoute("http://localhost/?section=hosts&view=whatever").view).toBe("");
  expect(readRoute("http://localhost/?section=atlantis").section).toBeNull();
});

test("module-owned tabs remain registered for deep links and the command palette", () => {
  expect(sections.find((section) => section.id === "models")?.views).toEqual(modelsViews);
  expect(sections.find((section) => section.id === "conversations")?.views).toEqual(conversationViews);
  expect(sections.find((section) => section.id === "tasks")?.views).toEqual(tasksViews);
  expect(sections.find((section) => section.id === "foundry")?.views).toEqual(foundryViews);
});

test("Learnings is retired and its old deep links open Foundry's learning page", () => {
  expect(sections.some((section) => section.id === "learnings")).toBe(false);
  const foundry = sections.find((section) => section.id === "foundry");
  expect(readRoute("http://localhost/?section=learnings")).toEqual({ section: foundry, view: "learning" });
  expect(readRoute("http://localhost/?section=learnings&view=memu")).toEqual({ section: foundry, view: "learning" });
  expect(readRoute("http://localhost/?section=foundry&view=experiments").view).toBe("experiments");
  // An unknown page falls back to the first one rather than to a blank frame.
  expect(readRoute("http://localhost/?section=foundry&view=nope").view).toBe("dashboard");
});

test("Orbit is retired and its old deep link resolves to SIM inside the same router", () => {
  expect(sections.some((section) => section.id === "orbit")).toBe(false);
  const sim = sections.find((section) => section.id === "sim");
  expect(sim?.label).toBe("SIM");
  // SIM replaced Orbit: an old bookmark lands on the section that owns that screen now.
  expect(readRoute("http://localhost/?section=orbit").section).toBe(sim);
  expect(readRoute("http://localhost/?section=orbit&view=runs")).toEqual({
    section: sim,
    view: "",
  });
  // The URL is left as it came: no second router rewriting what the operator typed.
  expect(readRoute("http://localhost/?section=sim").section).toBe(sim);
  expect(readRoute("http://localhost/?section=sim&view=whatever").view).toBe("");
});

test("the shell dictionary covers every section, group and view label", () => {
  const missing: string[] = [];
  for (const section of sections) {
    for (const text of [section.label, section.group]) {
      if (translateShell(text, "en") === text && !/^[A-Za-z0-9 .·]+$/.test(text))
        missing.push(text);
    }
    for (const [, label] of section.views)
      if (translateShell(label, "en") === label && !/^[A-Za-z0-9 .·]+$/.test(label))
        missing.push(label);
  }
  expect(missing, "shell strings without an English entry").toEqual([]);
  expect(shellDictionary["Conexiones"]).toBe("Connections");
  expect(translateShell("Conexiones", "es")).toBe("Conexiones");
  expect(translateShell("una cadena sin diccionario", "en")).toBe(
    "una cadena sin diccionario",
  );
});

test("every shell string a screen renders has an English entry", async () => {
  const files = ["App.tsx", "Settings.tsx", "Connections.tsx", "Palette.tsx"];
  const missing: string[] = [];
  for (const file of files) {
    const source = await readFile(new URL(`../../src/app/${file}`, import.meta.url), "utf8");
    for (const match of source.matchAll(/\bt\(\s*"([^"]+)"/g)) {
      const text = match[1];
      // Words that are the same in both languages (product names, "local") are fine.
      if (/^[A-Za-z0-9 .·:/_-]+$/.test(text)) continue;
      if (translateShell(text, "en") === text) missing.push(`${file}: ${text}`);
    }
  }
  expect(missing, "strings without an English entry").toEqual([]);
});

test("the light theme keeps secondary and error text at WCAG AA", async () => {
  const css = await readFile(new URL("../../src/styles/theme.css", import.meta.url), "utf8");
  const root = css.slice(css.indexOf(":root {"), css.indexOf(".dark {"));
  const token = (name: string) => {
    const match = root.match(new RegExp(`--${name}:\\s*(#[0-9a-fA-F]{6})`));
    if (!match) throw new Error(`--${name} is not a plain hex value in :root`);
    return match[1];
  };
  const luminance = (hex: string) => {
    const channels = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
    const linear = channels.map((v) =>
      v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4,
    );
    return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
  };
  const ratio = (a: string, b: string) => {
    const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x);
    return (high + 0.05) / (low + 0.05);
  };
  // Alert and note surfaces are the token over white at 5% and 10%: a tint of the
  // text colour itself, so it must be recomputed when the token moves.
  const tint = (color: string, alpha: number) =>
    "#" +
    [1, 3, 5]
      .map((i) =>
        Math.round(
          alpha * parseInt(color.slice(i, i + 2), 16) + (1 - alpha) * 255,
        )
          .toString(16)
          .padStart(2, "0"),
      )
      .join("");
  // Surfaces light text actually sits on: page, card, muted/pill surfaces and the
  // darkest warning surface used by the notes (primary/5 over white).
  for (const surface of ["background", "card", "muted", "secondary", "accent"]) {
    expect(
      ratio(token("muted-foreground"), token(surface)),
      `muted-foreground on --${surface}`,
    ).toBeGreaterThanOrEqual(4.5);
  }
  expect(
    ratio(token("muted-foreground"), tint(token("primary"), 0.05)),
    "muted text on the primary/5 note surface",
  ).toBeGreaterThanOrEqual(4.5);
  const destructive = token("destructive");
  expect(ratio(destructive, token("card"))).toBeGreaterThanOrEqual(4.5);
  for (const alpha of [0.05, 0.1])
    expect(
      ratio(destructive, tint(destructive, alpha)),
      `error text on the destructive/${alpha * 100} alert surface`,
    ).toBeGreaterThanOrEqual(4.5);
});
