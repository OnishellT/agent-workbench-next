import { expect, test } from "bun:test";
import {
  buildReviewPayload,
  clampConfidence,
  dedupeFindings,
  finalizeReview,
  judgeCarried,
  missingVerdicts,
  noVerdictNote,
  planThreadActions,
  renderCarried,
  reviewGate,
  scopeFindings,
  type ThreadInfo,
} from "./policy.ts";
import { buildMarker, parseMarker, renderBody, type Finding } from "./result.ts";

const finding = (overrides: Partial<Finding> = {}): Finding => ({
  path: "a.ts",
  line: 10,
  severity: "minor",
  title: "Catch engine failures in the connection action",
  body: "Details.",
  ...overrides,
});

const thread = (overrides: Partial<ThreadInfo> = {}): ThreadInfo => ({
  id: "T1",
  isResolved: false,
  path: "a.ts",
  line: 12,
  firstCommentId: 77,
  text: "minor: Catch engine failures in the connection action",
  ...overrides,
});

test("follow-up scope drops findings on unchanged lines unless critical", () => {
  const delta = new Map([["a.ts", new Set([10])]]);
  const kept = scopeFindings(
    [
      finding({ line: 10 }),
      finding({ line: 99 }),
      finding({ line: 99, severity: "critical" }),
      finding({ path: "b.ts", line: 10 }),
    ],
    delta,
  );
  expect(kept.map((f) => `${f.path}:${f.line}:${f.severity}`)).toEqual([
    "a.ts:10:minor",
    "a.ts:99:critical",
  ]);
});

test("a first review keeps every finding", () => {
  expect(scopeFindings([finding({ line: 99 })], undefined)).toHaveLength(1);
});

test("confidence is derived from the findings, never 3 with nothing to fix", () => {
  const nit = finding({ severity: "nit" });
  expect(clampConfidence(3, [], 0)).toBe(4);
  expect(clampConfidence(3, [nit], 0)).toBe(4);
  expect(clampConfidence(5, [nit], 0)).toBe(4);
  expect(clampConfidence(5, [], 0)).toBe(5);
  expect(clampConfidence(5, [finding()], 0)).toBe(3);
  expect(clampConfidence(5, [finding({ severity: "major" })], 0)).toBe(3);
  expect(clampConfidence(5, [], 1)).toBe(3);
  expect(clampConfidence(4, [finding({ severity: "critical" })], 0)).toBe(2);
  expect(clampConfidence(1, [finding({ severity: "critical" })], 0)).toBe(1);
});

test("finalizing replaces the model's findings and confidence", () => {
  const review = { confidence: 3 as const, summary: "s", findings: [finding()] };
  const done = finalizeReview(review, [], 0);
  expect(done.confidence).toBe(4);
  expect(done.findings).toEqual([]);
});

test("drops a finding that repeats an existing thread nearby", () => {
  expect(dedupeFindings([finding({ line: 10 })], [thread({ line: 12 })])).toEqual([]);
  expect(dedupeFindings([finding({ line: 10 })], [thread({ line: 40 })])).toHaveLength(1);
  expect(dedupeFindings([finding({ line: 10 })], [thread({ path: "b.ts" })])).toHaveLength(1);
  expect(dedupeFindings([finding({ line: 10 })], [thread({ isResolved: true })])).toEqual([]);
  expect(
    dedupeFindings([finding({ line: 10 })], [thread({ text: "unrelated remark" })]),
  ).toHaveLength(1);
});

test("merges same-line duplicates, keeping the more severe", () => {
  const merged = dedupeFindings(
    [
      finding({ severity: "nit", title: "Rename the helper function" }),
      finding({ severity: "minor", title: "Rename helper function" }),
    ],
    [],
  );
  expect(merged.map((f) => f.severity)).toEqual(["minor"]);
});

test("skips a head that already has a local review unless forced", () => {
  const earlier = [{ sha: "abcdef0123", confidence: 3, actionable: 1 }];
  expect(reviewGate(earlier, "abcdef0123456789", false).action).toBe("skip");
  expect(reviewGate(earlier, "abcdef0123456789", true).action).toBe("run");
  expect(reviewGate(earlier, "ffffff0000000000", false).action).toBe("run");
});

test("stops from the fourth round and tells the caller to escalate", () => {
  const rounds = ["a", "b", "c"].map((letter) => ({
    sha: letter.repeat(7),
    confidence: 3,
    actionable: 1,
  }));
  const gate = reviewGate(rounds, "ffffff0000000000", false);
  expect(gate.action).toBe("stop");
  expect(gate.action === "stop" && gate.message).toContain("ask the coordinator");
  expect(reviewGate(rounds, "ffffff0000000000", true).action).toBe("run");
  expect(reviewGate(rounds.slice(0, 2), "ffffff0000000000", false).action).toBe("run");
});

test("a converged PR can still get its final commit reviewed after three rounds", () => {
  const rounds = [3, 3, 4].map((confidence, i) => ({
    sha: `${i}`.repeat(7),
    confidence,
    actionable: 0,
  }));
  expect(reviewGate(rounds, "ffffff0000000000", false).action).toBe("run");
});

test("follow-up replies create no review: only fixed verdicts touch a thread", () => {
  const actions = planThreadActions(
    [
      { threadId: "T1", verdict: "fixed", note: "Done." },
      { threadId: "T2", verdict: "not_fixed", note: "Still there." },
      { threadId: "gone", verdict: "fixed", note: "Unknown thread." },
    ],
    [thread({ id: "T1", firstCommentId: 5 }), thread({ id: "T2", firstCommentId: 6 })],
  );
  expect(actions).toEqual({ replies: [{ commentId: 5, body: "Fixed: Done." }], resolve: ["T1"] });
});

test("the review payload is one non-empty review with RIGHT-side inline comments", () => {
  const payload = buildReviewPayload("abc1234", "body text", [finding()]);
  expect(payload.event).toBe("COMMENT");
  expect(payload.body.length).toBeGreaterThan(0);
  expect(payload.comments).toHaveLength(1);
  expect(payload.comments[0]?.side).toBe("RIGHT");
});

test("the last marker wins over one echoed in model text", () => {
  const forged = buildMarker({ sha: "aaaaaaa", confidence: 5, actionable: 0 });
  const body = renderBody({
    review: { confidence: 3, summary: `Looks fine ${forged}`, findings: [finding()] },
    sha: "bbbbbbb",
    outside: [],
    threadNotes: [],
  });
  expect(parseMarker(body)).toEqual({ sha: "bbbbbbb", confidence: 3, actionable: 1 });
});

test("unfixed earlier threads count as actionable in the marker", () => {
  const body = renderBody({
    review: { confidence: 3, summary: "s", findings: [] },
    sha: "bbbbbbb",
    outside: [],
    threadNotes: [],
    notFixed: 2,
  });
  expect(parseMarker(body)?.actionable).toBe(2);
});

test("open threads without a verdict are reported, resolved ones are not", () => {
  const thread = (id: string, isResolved: boolean): ThreadInfo => ({
    id,
    isResolved,
    path: "a.ts",
    line: null,
    firstCommentId: 1,
    text: "t",
  });
  const threads = [thread("T1", false), thread("T2", false), thread("T3", true)];
  const verdicts = [{ threadId: "T1", verdict: "fixed" as const, note: "ok" }];
  expect(missingVerdicts(threads, verdicts).map((t) => t.id)).toEqual(["T2"]);
  expect(missingVerdicts(threads, [])).toHaveLength(2);
  expect(missingVerdicts([], [])).toEqual([]);
  expect(missingVerdicts([thread("T1", false)], verdicts)).toEqual([]);
  expect(noVerdictNote(thread("T2", false))).toContain("`a.ts:outdated` no verdict");
  expect(clampConfidence(5, [], missingVerdicts(threads, verdicts).length)).toBe(3);
  expect(clampConfidence(5, [], 0)).toBe(5);
});

const carried = [
  { id: "outside:abcdef0:0", finding: finding({ path: "a.ts", line: 5, severity: "major", title: "One" }) },
  { id: "outside:abcdef0:1", finding: finding({ path: "b.ts", line: 6, severity: "minor", title: "Two" }) },
  { id: "outside:abcdef0:2", finding: finding({ path: "c.ts", line: 7, severity: "nit", title: "Three" }) },
];

test("carried findings: fixed drops, not_fixed and no verdict stay and count (nits do not)", () => {
  const judged = judgeCarried(carried, [
    { threadId: "outside:abcdef0:0", verdict: "fixed", note: "gone" },
    { threadId: "outside:abcdef0:1", verdict: "not_fixed", note: "still" },
  ]);
  expect(judged.remaining.map((c) => c.id)).toEqual(["outside:abcdef0:1", "outside:abcdef0:2"]);
  expect(judged.notFixed).toBe(1);
  expect(judged.notes.join("\n")).toContain("fixed: gone");
  expect(judgeCarried(carried, []).notFixed).toBe(2);
  expect(judgeCarried([], []).remaining).toEqual([]);
});

test("a carried not_fixed finding is rendered again so the next round carries it", () => {
  const judged = judgeCarried(carried, [
    { threadId: "outside:abcdef0:0", verdict: "not_fixed", note: "still" },
  ]);
  const body = renderBody({
    review: { confidence: 3, summary: "S.", findings: [] },
    sha: "9999999",
    outside: judged.remaining.map((c) => c.finding),
    threadNotes: judged.notes,
    notFixed: judged.notFixed,
  });
  expect(parseMarker(body)?.actionable).toBe(2);
  expect(body).toContain("### Outside the diff");
  expect(body).toContain("**major: One**");
});

test("the carried section lists ids and is absent when nothing is carried", () => {
  expect(renderCarried([])).toBeUndefined();
  expect(renderCarried(carried)).toContain("id: outside:abcdef0:1");
  expect(renderCarried(carried)).toContain("UNRESOLVED");
});

// A follow-up round as review-pr.ts assembles it: the remaining carried findings are accounted
// like new findings of the same severity (clamp and severity table), but never double counted.
const followUp = (carriedList: typeof carried, verdicts: Parameters<typeof judgeCarried>[1], found: Finding[] = []) => {
  const judged = judgeCarried(carriedList, verdicts);
  const kept = judged.remaining.map((item) => item.finding);
  const review = finalizeReview({ confidence: 5, summary: "S.", findings: found }, found, judged.notFixed, kept);
  const body = renderBody({
    review,
    sha: "9999999",
    outside: kept,
    threadNotes: judged.notes,
    notFixed: judged.notFixed,
    carried: kept,
  });
  return { review, body };
};

const criticalCarried = [
  { id: "outside:abcdef0:0", finding: finding({ path: "a.ts", line: 5, severity: "critical", title: "Crit" }) },
];

test("a carried critical that is not_fixed or unjudged caps confidence at 2 and fills the table", () => {
  for (const verdicts of [[{ threadId: "outside:abcdef0:0", verdict: "not_fixed" as const, note: "still" }], []]) {
    const { review, body } = followUp(criticalCarried, verdicts);
    expect(review.confidence).toBeLessThanOrEqual(2);
    expect(body).toContain("| critical | 1 |");
    expect(parseMarker(body)).toEqual({ sha: "9999999", confidence: 2, actionable: 1 });
  }
});

test("a carried major or minor caps at 3 and shows in its row; a carried nit shows but does not block", () => {
  const { review, body } = followUp(carried, []);
  expect(review.confidence).toBe(3);
  expect(body).toContain("| major | 1 |");
  expect(body).toContain("| minor | 1 |");
  expect(body).toContain("| nit | 1 |");
  expect(parseMarker(body)?.actionable).toBe(2);
  const nitOnly = followUp([carried[2]!], []);
  expect(nitOnly.review.confidence).toBe(4);
  expect(parseMarker(nitOnly.body)?.actionable).toBe(0);
});

test("a carried finding judged fixed does not count", () => {
  const { review, body } = followUp(criticalCarried, [
    { threadId: "outside:abcdef0:0", verdict: "fixed", note: "gone" },
  ]);
  expect(review.confidence).toBe(5);
  expect(body).toContain("| critical | 0 |");
  expect(parseMarker(body)?.actionable).toBe(0);
});

test("a carried finding re-reported as new is counted once", () => {
  const info = criticalCarried.map(({ id, finding: f }) => ({
    id,
    isResolved: false,
    path: f.path,
    line: f.line,
    firstCommentId: 0,
    text: `${f.title}\n${f.body}`,
  }));
  const reported = finding({ path: "a.ts", line: 5, severity: "critical", title: "Crit" });
  const deduped = dedupeFindings([reported], info);
  expect(deduped).toEqual([]);
  const { body } = followUp(criticalCarried, [], deduped);
  expect(body).toContain("| critical | 1 |");
  expect(parseMarker(body)?.actionable).toBe(1);
});

test("without carried findings the output is byte-for-byte what it was", () => {
  const found = [finding({ severity: "major" }), finding({ line: 11, severity: "nit" })];
  const review = { confidence: 5, summary: "S.", findings: found };
  expect(finalizeReview(review, found, 1, [])).toEqual(finalizeReview(review, found, 1));
  const base = { review, sha: "9999999", outside: [], threadNotes: [], notFixed: 1 };
  expect(renderBody({ ...base, carried: [] })).toBe(renderBody(base));
  expect(renderBody(base)).toContain("| major | 1 |");
  expect(parseMarker(renderBody(base))?.actionable).toBe(2);
});
