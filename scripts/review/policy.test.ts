import { expect, test } from "bun:test";
import {
  buildReviewPayload,
  clampConfidence,
  dedupeFindings,
  finalizeReview,
  planThreadActions,
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
