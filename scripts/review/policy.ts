// Pure review policy: what gets posted, how confidence is derived, what is a duplicate,
// when a run is skipped or stopped, and which thread actions follow the verdicts.
import {
  inlineCommentBody,
  type CarriedFinding,
  type Finding,
  type Marker,
  type Resolution,
  type Review,
  type Severity,
} from "./result.ts";

export interface ThreadInfo {
  id: string;
  isResolved: boolean;
  path: string;
  line: number | null;
  firstCommentId: number;
  text: string;
}

const severityRank: Record<Severity, number> = { critical: 3, major: 2, minor: 1, nit: 0 };

// Follow-up rounds judge the delta: a finding on code that did not change since the last
// reviewed commit survives only when it is critical.
export function scopeFindings(
  findings: readonly Finding[],
  deltaLines: Map<string, Set<number>> | undefined,
): Finding[] {
  if (deltaLines === undefined) return [...findings];
  return findings.filter(
    (finding) => finding.severity === "critical" || deltaLines.get(finding.path)?.has(finding.line),
  );
}

function words(text: string): Set<string> {
  return new Set(text.toLowerCase().match(/[a-z0-9_]{3,}/g) ?? []);
}

function overlap(a: Set<string>, b: Set<string>): number {
  if (a.size === 0) return 0;
  return [...a].filter((word) => b.has(word)).length / a.size;
}

const lineWindow = 3;

// Drops findings already raised in an earlier thread (same place, similar title) and merges
// same-line duplicates within the run, keeping the more severe one.
export function dedupeFindings(
  findings: readonly Finding[],
  threads: readonly ThreadInfo[],
): Finding[] {
  const kept: Finding[] = [];
  for (const finding of findings) {
    const title = words(finding.title);
    const known = threads.some(
      (thread) =>
        thread.path === finding.path &&
        (thread.line === null || Math.abs(thread.line - finding.line) <= lineWindow) &&
        overlap(title, words(thread.text)) >= 0.6,
    );
    if (known) continue;
    const twin = kept.findIndex(
      (other) =>
        other.path === finding.path &&
        other.line === finding.line &&
        overlap(title, words(other.title)) >= 0.5,
    );
    if (twin === -1) kept.push(finding);
    else if (severityRank[finding.severity] > severityRank[kept[twin]?.severity ?? "nit"]) {
      kept[twin] = finding;
    }
  }
  return kept;
}

// Confidence follows from the findings, not from the model's mood: any critical caps it at 2,
// any major, minor or unfixed earlier thread at 3, a clean review is at least 4, and 5 needs no nits.
export function clampConfidence(
  confidence: number,
  findings: readonly Finding[],
  notFixed: number,
): number {
  const count = (severity: Severity) => findings.filter((f) => f.severity === severity).length;
  const clamp = (low: number, high: number) => Math.min(high, Math.max(low, confidence));
  if (count("critical") > 0) return clamp(1, 2);
  if (count("major") + count("minor") + notFixed > 0) return clamp(1, 3);
  return count("nit") > 0 ? 4 : clamp(4, 5);
}

export function finalizeReview(
  review: Review,
  findings: readonly Finding[],
  notFixed: number,
  carried: readonly Finding[] = [],
): Review {
  // Carried findings still open weigh like new ones of the same severity, but stay out of
  // `review.findings`, which only holds what this round reports.
  return {
    ...review,
    findings,
    confidence: clampConfidence(review.confidence, [...findings, ...carried], notFixed),
  };
}

// Every open earlier thread needs a verdict: an open thread the reviewer left out counts as
// not fixed instead of passing as fixed.
export function missingVerdicts(
  threads: readonly ThreadInfo[],
  verdicts: readonly Resolution[],
): ThreadInfo[] {
  const answered = new Set(verdicts.map((verdict) => verdict.threadId));
  return threads.filter((thread) => !thread.isResolved && !answered.has(thread.id));
}

export function noVerdictNote(thread: ThreadInfo): string {
  return `- \`${thread.path}:${thread.line ?? "outdated"}\` no verdict: the reviewer did not judge this open thread, so it counts as not fixed.`;
}

export interface CarriedJudgement {
  remaining: CarriedFinding[];
  notFixed: number;
  notes: string[];
}

// Carried findings (outside the diff, no thread) follow the thread rule: `fixed` drops one,
// `not_fixed` or no verdict keeps it, to be rendered again for the next round. A nit stays visible
// but never counts as not fixed, because nits never block.
export function judgeCarried(
  carried: readonly CarriedFinding[],
  verdicts: readonly Resolution[],
): CarriedJudgement {
  const byId = new Map(verdicts.map((verdict) => [verdict.threadId, verdict]));
  const judgement: CarriedJudgement = { remaining: [], notFixed: 0, notes: [] };
  for (const { id, finding } of carried) {
    const verdict = byId.get(id);
    const where = `\`${finding.path}:${finding.line}\``;
    if (verdict?.verdict === "fixed") {
      judgement.notes.push(`- ${where} fixed: ${verdict.note}`);
      continue;
    }
    judgement.remaining.push({ id, finding });
    if (finding.severity !== "nit") judgement.notFixed++;
    judgement.notes.push(
      verdict === undefined
        ? `- ${where} no verdict: the reviewer did not judge this earlier finding, so it counts as not fixed.`
        : `- ${where} not fixed: ${verdict.note}`,
    );
  }
  return judgement;
}

// Context block for the reviewer: each carried finding as an UNRESOLVED item with its id.
export function renderCarried(carried: readonly CarriedFinding[]): string | undefined {
  if (carried.length === 0) return undefined;
  return carried
    .map(
      ({ id, finding }) =>
        `id: ${id}\nstate: UNRESOLVED\nlocation: ${finding.path}:${finding.line}\n  ${finding.severity}: ${finding.title}\n  ${finding.body.replace(/\n/g, "\n  ")}`,
    )
    .join("\n\n");
}

export const maxLocalRounds = 3;

export type Gate = { action: "run" } | { action: "skip" | "stop"; message: string };

export function reviewGate(earlier: readonly Marker[], headSha: string, force: boolean): Gate {
  if (force) return { action: "run" };
  if (earlier.some((marker) => headSha.startsWith(marker.sha) || marker.sha.startsWith(headSha))) {
    return {
      action: "skip",
      message: `A local review of ${headSha.slice(0, 7)} already exists; nothing to do (use --force to review again).`,
    };
  }
  // A PR that converged (last confidence 4 or more) can still get its final commit reviewed.
  if (earlier.length >= maxLocalRounds && (earlier.at(-1)?.confidence ?? 0) < 4) {
    return {
      action: "stop",
      message: `${earlier.length} local reviews have not converged: stop and ask the coordinator before another round (use --force to override).`,
    };
  }
  return { action: "run" };
}

export interface ThreadActions {
  replies: { commentId: number; body: string }[];
  resolve: string[];
}

// Only a fixed verdict touches a thread (a short reply and a resolve); not-fixed verdicts
// live in the review body, so follow-up rounds create no extra reviews or comments.
export function planThreadActions(
  verdicts: readonly Resolution[],
  threads: readonly ThreadInfo[],
): ThreadActions {
  const open = new Map(threads.filter((t) => !t.isResolved).map((t) => [t.id, t]));
  const actions: ThreadActions = { replies: [], resolve: [] };
  for (const verdict of verdicts) {
    const thread = open.get(verdict.threadId);
    if (!thread || verdict.verdict !== "fixed") continue;
    actions.replies.push({ commentId: thread.firstCommentId, body: `Fixed: ${verdict.note}` });
    actions.resolve.push(thread.id);
  }
  return actions;
}

export interface ReviewPayload {
  commit_id: string;
  event: "COMMENT";
  body: string;
  comments: { path: string; line: number; side: "RIGHT"; body: string }[];
}

export function buildReviewPayload(
  sha: string,
  body: string,
  inline: readonly Finding[],
): ReviewPayload {
  return {
    commit_id: sha,
    event: "COMMENT",
    body,
    comments: inline.map((finding) => ({
      path: finding.path,
      line: finding.line,
      side: "RIGHT",
      body: inlineCommentBody(finding),
    })),
  };
}
