import { expect, test } from "@playwright/test";
import {
  CONVERSATIONS_API,
  EXAMPLE_API,
  FOUNDRY_API,
  TASKS_API,
  conversationQuery,
  conversationViews,
  coverageFlags,
  coverageStatusLabels,
  emptyFilters,
  emptyFocus,
  filtersURL,
  focusURL,
  formatBytes,
  fromDisplayIndex,
  inertNote,
  instant,
  jobStatusLabels,
  scanStatusLabels,
  mergePage,
  path,
  quoteText,
  rangeProblem,
  acceptanceProblem,
  configNumberProblem,
  configRanges,
  formatCommand,
  formatUsage,
  parseCommand,
  proposalActive,
  proposalStatusLabels,
  readFilters,
  readFocus,
  revisionOptions,
  skippedReasonLabels,
  taskConfigPatch,
  taskFilters,
  taskLabels,
  taskState,
  toDisplayIndex,
  viewOf,
  wholeRange,
} from "../../src/modules/conversations/domain";
import { translate } from "../../src/modules/conversations/i18n";

// Pure contract checks of the M05 module (Conversaciones, Fuentes, Tareas): no browser
// and no backend. They run with the Playwright runner alone, e.g.
//   bun run test tests/e2e/conversations-contracts.spec.ts
const BASE = "http://workbench.invalid/?section=conversations&view=sources&host=srv-1";

test("M05 keeps the routes the backend mounted", () => {
  expect(CONVERSATIONS_API).toBe("/api/conversations");
  expect(TASKS_API).toBe("/api/tasks");
  expect(FOUNDRY_API).toBe("/api/foundry");
  expect(EXAMPLE_API).toBe("/api/foundry/example");
  expect(path(CONVERSATIONS_API, ["sources", "s 1/x"])).toBe(
    "/api/conversations/sources/s%201%2Fx",
  );
  expect(path(CONVERSATIONS_API, ["c-1", "revisions", "rev-2"], "?compact=1")).toBe(
    "/api/conversations/c-1/revisions/rev-2?compact=1",
  );
  expect(path(FOUNDRY_API, ["jobs", "job-1"])).toBe("/api/foundry/jobs/job-1");
});

test("the list query only carries the filters the operator set", () => {
  expect(conversationQuery(emptyFilters)).toBe("?limit=50");
  const query = conversationQuery(
    { q: "persistencia http", source: "s-1", adapter: "codex", status: "partial" },
    "100",
    25,
  );
  const params = new URLSearchParams(query.replace(/^\?/, ""));
  expect(params.get("q")).toBe("persistencia http");
  expect(params.get("source_id")).toBe("s-1");
  expect(params.get("adapter")).toBe("codex");
  expect(params.get("status")).toBe("partial");
  expect(params.get("limit")).toBe("25");
  expect(params.get("cursor")).toBe("100");
});

test("filters and deep links keep the shell route they were given", () => {
  const filters = readFilters(BASE);
  expect(filters).toEqual(emptyFilters);
  const withFilters = filtersURL(BASE, { ...emptyFilters, q: "sqlite", status: "complete" });
  expect(withFilters.searchParams.get("section")).toBe("conversations");
  expect(withFilters.searchParams.get("view")).toBe("sources");
  expect(withFilters.searchParams.get("host")).toBe("srv-1");
  expect(withFilters.searchParams.get("q")).toBe("sqlite");
  expect(readFilters(withFilters).status).toBe("complete");

  const cleared = filtersURL(withFilters, emptyFilters);
  expect(cleared.searchParams.get("q")).toBeNull();
  expect(cleared.searchParams.get("section")).toBe("conversations");

  const focus = focusURL(withFilters, { conversation: "c-1", revision: "rev-1", event: "7" });
  expect(readFocus(focus)).toEqual({ conversation: "c-1", revision: "rev-1", event: "7" });
  expect(focus.searchParams.get("section")).toBe("conversations");
  expect(readFocus(focusURL(focus, emptyFocus))).toEqual(emptyFocus);
  expect(focusURL(focus, emptyFocus).searchParams.get("section")).toBe("conversations");
});

test("an unknown view falls back to the first one and never to another module", () => {
  expect(viewOf("sources")).toBe("sources");
  expect(viewOf("example")).toBe("example");
  expect(viewOf("learning")).toBe("conversations");
  expect(viewOf(undefined)).toBe("conversations");
  expect(conversationViews.map(([id]) => id)).toEqual(["conversations", "sources", "example"]);
});

test("the detail envelope turns into revision options without inventing a revision", () => {
  // The route answers {conversation, revisions} with RevisionDTO.revision: an option is
  // built from that field, and an undefined list never yields a phantom option.
  expect(revisionOptions(undefined, "", "es")).toEqual([]);
  const options = revisionOptions(
    [
      { revision: "d7aa21e690d68edf", created: 1_700_000_000, coverage: { status: "complete" } },
      { revision: "aa11bb22cc33dd44", created: 1_700_000_500, coverage: { status: "partial" } },
    ],
    "d7aa21e690d68edf",
    "es",
  );
  expect(options.map((option) => option.value)).toEqual([
    "d7aa21e690d68edf",
    "aa11bb22cc33dd44",
  ]);
  expect(options[0].label).toContain("d7aa21e6");
  expect(options[0].label).toContain("completa");
  expect(options[1].label).toContain("parcial");
  const unknown = revisionOptions([{ revision: "ff00", coverage: { status: "rare" } }], "ff00", "en");
  expect(unknown[0].label).toContain("rare");
  // A deep link to a revision the envelope does not list keeps exactly one extra option.
  const extra = revisionOptions([{ revision: "aa11" }], "zz99", "es");
  expect(extra.map((option) => option.value)).toEqual(["aa11", "zz99"]);
  expect(extra.filter((option) => option.value === "aa11")).toHaveLength(1);
});

test("a range is only sendable inside its revision", () => {
  expect(rangeProblem({ first: 0, last: 9 }, 10)).toBeNull();
  expect(rangeProblem({ first: 9, last: 3 }, 10)).toContain("fin no puede ser anterior");
  expect(rangeProblem({ first: 0, last: 10 }, 10)).toContain("no puede pasar del 10");
  expect(rangeProblem({ first: -1, last: 3 }, 10)).toContain("enteros");
  expect(rangeProblem({ first: 1.5, last: 3 }, 10)).toContain("enteros");
  expect(rangeProblem({ first: 0, last: 0 }, 0)).toContain("no tiene eventos");
  expect(wholeRange(0)).toEqual({ first: 0, last: 0 });
  expect(wholeRange(3)).toEqual({ first: 0, last: 2 });
  expect(toDisplayIndex(0)).toBe(1);
  expect(fromDisplayIndex(1)).toBe(0);
  expect(fromDisplayIndex(toDisplayIndex(41))).toBe(41);
});

test("cursor pages append without repeating a row", () => {
  const first = [{ id: "a" }, { id: "b" }];
  const second = [{ id: "b" }, { id: "c" }];
  expect(mergePage(first, second, (row) => row.id).map((row) => row.id)).toEqual([
    "a",
    "b",
    "c",
  ]);
  expect(mergePage(first, undefined, (row) => row.id)).toEqual(first);
});

test("both human gates keep their own state", () => {
  const task = { first_event: 0, last_event: 1, session_id: "s", revision: "r", id: "t" };
  expect(taskState({ ...task, completed: false, label: "correct" })).toBe("incomplete");
  expect(taskState({ ...task, completed: true, label: "pending" })).toBe("pending");
  expect(taskState({ ...task, completed: true, label: "correct" })).toBe("correct");
  expect(taskState({ ...task, completed: true, label: "incorrect" })).toBe("incorrect");
  expect(taskState({ ...task, completed: true })).toBe("pending");
  expect(taskFilters.map(([value]) => value)).toEqual([
    "all",
    "incomplete",
    "pending",
    "correct",
    "incorrect",
  ]);
  expect(taskLabels.map(([value]) => value)).toEqual(["pending", "correct", "incorrect"]);
});

test("coverage is never read as complete when it is not", () => {
  const complete = coverageFlags({ status: "complete", event_count: 12, bytes: 2048 });
  expect(complete.partial).toBe(false);
  expect(complete.events).toBe(12);
  const partial = coverageFlags({
    status: "partial",
    blocking_issue_count: 1,
    semantic_issue_count: 2,
    issue_count: 3,
  });
  expect(partial.partial).toBe(true);
  expect([partial.blocking, partial.semantic, partial.issues]).toEqual([1, 2, 3]);
  const unknown = coverageFlags(undefined);
  expect(unknown.status).toBe("unknown");
  expect(unknown.partial).toBe(false);
  expect(unknown.events).toBeNull();
  expect(unknown.bytes).toBeNull();
});

test("backend values are named through label maps, not as raw translation keys", () => {
  expect(Object.keys(coverageStatusLabels)).toEqual(["complete", "partial", "error", "unknown"]);
  expect(translate(coverageStatusLabels.error, "en")).toBe("with errors");
  expect(translate(coverageStatusLabels.unknown, "es")).toBe("sin dato");
  expect(Object.keys(jobStatusLabels)).toContain("succeeded");
  expect(translate(jobStatusLabels.succeeded, "en")).toBe("Completed");
  // The last scan of a source publishes the capture outcome: it is named too, so a
  // partial capture never shows an English enum inside a Spanish screen.
  expect(Object.keys(scanStatusLabels)).toContain("succeeded");
  expect(translate(scanStatusLabels.partial, "en")).toBe("partial");
  expect(translate(scanStatusLabels.complete, "es")).toBe("completa");
});

test("timestamps and sizes come from what the installation reported", () => {
  expect(instant(1_700_000_000, "es")).not.toBe("—");
  expect(instant(1_700_000_000_000, "es")).not.toBe("—");
  expect(instant("2026-03-10T12:00:00Z", "en")).not.toBe("—");
  expect(instant(undefined, "es")).toBe("—");
  expect(instant("", "es")).toBe("—");
  expect(instant("no es una fecha", "es")).toBe("—");
  expect(instant(0, "es")).toBe("—");
  expect(formatBytes(2048, "en")).toBe("2.0 KiB");
  expect(formatBytes(0)).toBe("0 B");
  expect(formatBytes(undefined)).toBeNull();
});

test("transcript text is quoted, never interpreted", () => {
  expect(quoteText("  una\n  línea   larga ")).toBe("una línea larga");
  expect(quoteText("x".repeat(30), 10)).toBe(`${"x".repeat(10)}…`);
  expect(inertNote({ event_index: 0, inert: true })).toContain("no se ejecuta");
  expect(inertNote({ event_index: 0, event_text_truncated: true })).toContain("Vista abreviada");
  expect(inertNote({ event_index: 0, text: "hola" })).toBeNull();
});

test("the analysis configuration only sends what really changed", () => {
  const provider = {
    kind: "codex",
    command: ["codex"],
    model: "gpt-6-luna",
    timeout: 120,
    real_inference: true,
    provider: "openai-codex",
    reasoning: "high",
  };
  const current = {
    auto_tasks: false,
    task_idle_seconds: 120,
    max_input_chars: 120000,
    max_calls: 8,
    experiment_seconds: 900,
    provider,
    replay: { ...provider, kind: "pi" },
  };
  expect(taskConfigPatch(current, current)).toEqual({});
  const next = { ...current, auto_tasks: true, max_calls: 12, provider: { ...provider, model: "gpt-6-sol" } };
  expect(taskConfigPatch(current, next)).toEqual({
    auto_tasks: true,
    max_calls: 12,
    provider: { model: "gpt-6-sol" },
  });
  const replayOnly = { ...current, replay: { ...current.replay, command: ["pi", "--nogui"], real_inference: false } };
  expect(taskConfigPatch(current, replayOnly)).toEqual({
    replay: { command: ["pi", "--nogui"], real_inference: false },
  });
});

test("the budget fields keep the ranges the server enforces", () => {
  expect(Object.keys(configRanges)).toEqual([
    "task_idle_seconds",
    "max_input_chars",
    "max_calls",
    "experiment_seconds",
  ]);
  expect(configRanges.max_calls).toEqual([2, 200]);
  expect(configNumberProblem("max_calls", "8")).toBeNull();
  expect(configNumberProblem("max_calls", "2")).toBeNull();
  expect(configNumberProblem("max_calls", "200")).toBeNull();
  expect(configNumberProblem("max_calls", "1")).toContain("entre 2 y 200");
  expect(configNumberProblem("max_calls", "201")).toContain("entre 2 y 200");
  expect(configNumberProblem("max_calls", "ocho")).toContain("número entero");
  expect(configNumberProblem("max_calls", "")).toContain("número entero");
  expect(configNumberProblem("task_idle_seconds", "14")).toContain("entre 15 y 86400");
  expect(configNumberProblem("experiment_seconds", "28801")).toContain("entre 30 y 28800");
  expect(configNumberProblem("max_input_chars", "999")).toContain("entre 1000 y 500000");
});

test("the provider command round-trips as the argv the server resolves", () => {
  expect(parseCommand("")).toEqual([]);
  expect(parseCommand("  codex  --model  ")).toEqual(["codex", "--model"]);
  expect(formatCommand(["codex", "--model"])).toBe("codex --model");
  expect(parseCommand(formatCommand(["pi", "--nogui"]))).toEqual(["pi", "--nogui"]);
  expect(formatCommand(undefined)).toBe("");
});

test("a proposal is only acceptable with a verifiable real-inference receipt", () => {
  expect(proposalActive("queued")).toBe(true);
  expect(proposalActive("running")).toBe(true);
  expect(proposalActive("completed")).toBe(false);
  expect(Object.keys(proposalStatusLabels)).toEqual([
    "queued",
    "running",
    "completed",
    "failed",
    "cancelled",
    "interrupted",
  ]);
  expect(acceptanceProblem({ status: "running" })).toContain("todavía no está completa");
  expect(acceptanceProblem({ status: "completed", inference: { runs: [] } })).toContain(
    "recibo verificable de inferencia real",
  );
  expect(
    acceptanceProblem({
      status: "completed",
      inference: { runs: [{ real_inference: false }, { real_inference: true }] },
    }),
  ).toBeNull();
  expect(skippedReasonLabels.already_represented).toContain("Ya representada");
  expect(formatUsage({ input_tokens: 120, output_tokens: 8 })).toBe(
    "input_tokens 120 · output_tokens 8",
  );
  expect(formatUsage({})).toBeNull();
  expect(formatUsage(undefined)).toBeNull();
});

test("a missing translation stays Spanish instead of inventing a value", () => {
  expect(translate("Citas fijadas", "en")).toBe("Pinned citations");
  expect(translate("Estado", "en")).toBe("State");
  // The house translator works per known phrase: an unknown sentence keeps its words
  // and only the entries of the dictionary are replaced, never the whole string.
  expect(translate("Texto sin clave de traducción", "en")).toBe(
    "Texto sin clave de traducción",
  );
  expect(translate("Eventos sin clave", "en")).toBe("Events sin clave");
  expect(translate("Citas fijadas", "es")).toBe("Citas fijadas");
});
