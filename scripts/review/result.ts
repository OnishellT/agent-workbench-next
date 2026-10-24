// The reviewer's structured result: schema, extraction, mapping onto the diff, and the
// text posted to the pull request.
export type Severity = "critical" | "major" | "minor" | "nit";
const severities: readonly string[] = ["critical", "major", "minor", "nit"];

export interface Finding {
  path: string;
  line: number;
  severity: Severity;
  title: string;
  body: string;
  suggestion?: string;
}

export interface Resolution {
  threadId: string;
  verdict: "fixed" | "not_fixed";
  note: string;
}

export interface Review {
  confidence: number;
  summary: string;
  findings: readonly Finding[];
  resolved?: readonly Resolution[];
}

// Strict structural validation of the reviewer's JSON: required fields, integer lines, confidence
// 1-5, and no unknown properties anywhere (a model that invents fields has misread the contract).
type Fields = Record<string, unknown>;

function object(value: unknown, where: string, allowed: readonly string[]): Fields {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${where} must be an object`);
  }
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error(`${where} has an unknown property "${key}"`);
  }
  return value as Fields;
}

function text(fields: Fields, key: string, where: string): string {
  const value = fields[key];
  if (typeof value !== "string") throw new Error(`${where}.${key} must be a string`);
  return value;
}

function list(fields: Fields, key: string, where: string): unknown[] {
  const value = fields[key];
  if (!Array.isArray(value)) throw new Error(`${where}.${key} must be an array`);
  return value;
}

function validateFinding(value: unknown, index: number): Finding {
  const where = `findings[${index}]`;
  const fields = object(value, where, ["path", "line", "severity", "title", "body", "suggestion"]);
  const line = fields["line"];
  const severity = text(fields, "severity", where);
  if (typeof line !== "number" || !Number.isInteger(line)) {
    throw new Error(`${where}.line must be an integer`);
  }
  if (!severities.includes(severity)) throw new Error(`${where}.severity "${severity}" is not one of ${severities.join(", ")}`);
  const finding: Finding = {
    path: text(fields, "path", where),
    line,
    severity: severity as Severity,
    title: text(fields, "title", where),
    body: text(fields, "body", where),
  };
  if (fields["suggestion"] !== undefined) finding.suggestion = text(fields, "suggestion", where);
  return finding;
}

function validateResolution(value: unknown, index: number): Resolution {
  const where = `resolved[${index}]`;
  const fields = object(value, where, ["threadId", "verdict", "note"]);
  const verdict = text(fields, "verdict", where);
  if (verdict !== "fixed" && verdict !== "not_fixed") {
    throw new Error(`${where}.verdict must be "fixed" or "not_fixed"`);
  }
  return { threadId: text(fields, "threadId", where), verdict, note: text(fields, "note", where) };
}

export function validateReview(value: unknown): Review {
  const fields = object(value, "review", ["confidence", "summary", "findings", "resolved"]);
  const confidence = fields["confidence"];
  if (typeof confidence !== "number" || !Number.isInteger(confidence) || confidence < 1 || confidence > 5) {
    throw new Error("review.confidence must be an integer from 1 to 5");
  }
  const review: Review = {
    confidence,
    summary: text(fields, "summary", "review"),
    findings: list(fields, "findings", "review").map(validateFinding),
  };
  if (fields["resolved"] !== undefined) {
    review.resolved = list(fields, "resolved", "review").map(validateResolution);
  }
  return review;
}

export type Decoded = { ok: true; review: Review } | { ok: false; error: string };

// The result is the last fenced block of the answer (```json preferred).
export function extractJson(answer: string): string | undefined {
  const blocks = [...answer.matchAll(/^```(\w*)[^\S\n]*\n([\s\S]*?)^```[^\S\n]*$/gm)];
  const json = blocks.filter((block) => block[1] === "json");
  const pick = (json.length > 0 ? json : blocks).at(-1);
  return pick?.[2]?.trim();
}

// Words that would name the tooling in posted text.
const forbiddenWord = /\b(pi|deepseek|opencode|claude|anthropic|openai|gpt|llm|ai|chatgpt)\b/i;
const forbiddenPhrase = /generated (?:by|with)|as an? (?:language model|assistant)/i;

// Code spans and file names (`CLAUDE.md`, `scripts/review/pi-stream.ts`) are not prose.
const fileLike = /`[^`]*`|[\w.-]*\/[\w./-]*|[\w-]+\.(?:ts|tsx|js|go|py|md|json|sh|yml|yaml|css|toml)\b/g;

export function findNeutralityViolation(text: string): string | undefined {
  const prose = text.replace(fileLike, " ");
  return forbiddenWord.exec(prose)?.[0] ?? forbiddenPhrase.exec(prose)?.[0];
}

// The reviewer reads untrusted text and can read local files, so nothing that contains one of
// the owner's secrets is ever posted.
export function findSecretLeak(text: string, secrets: readonly string[]): boolean {
  return secrets.some((secret) => secret.length >= 8 && text.includes(secret));
}

// Removes HTML comments, then any delimiter left over, until nothing changes. A single pass can
// rebuild a comment from its own remains (`<<!---->!-- ... -->`), and a rebuilt review marker must
// never reach the posted text or the model's context.
export function stripHtmlComments(text: string): string {
  let current = text;
  for (;;) {
    const next = current.replace(/<!--[\s\S]*?-->/g, "").replace(/<!--/g, "").replace(/-->/g, "");
    if (next === current) return next;
    current = next;
  }
}

export function decodeReview(answer: string): Decoded {
  const json = extractJson(answer);
  if (json === undefined) return { ok: false, error: "No fenced JSON block found in the answer." };
  let value: unknown;
  try {
    // HTML comments are dropped so that a marker echoed by the model is never mistaken for the real one.
    // A null property is read as an absent one: the model often writes `"suggestion": null`.
    value = JSON.parse(stripHtmlComments(json), (_key, field: unknown) =>
      field === null ? undefined : field,
    );
  } catch (cause) {
    return { ok: false, error: `The JSON block does not parse: ${String(cause)}` };
  }
  let review: Review;
  try {
    review = validateReview(value);
  } catch (cause) {
    return { ok: false, error: `The JSON does not match the schema: ${String(cause)}` };
  }
  // Prose only: paths and suggested code legitimately contain names such as CLAUDE.md.
  const text = [
    review.summary,
    ...review.findings.flatMap((finding) => [finding.title, finding.body]),
    ...(review.resolved ?? []).map((resolution) => resolution.note),
  ].join("\n");
  const word = findNeutralityViolation(text);
  if (word !== undefined) {
    return {
      ok: false,
      error: `The text mentions "${word}". Posted text must not name any tool, model, vendor or author; rewrite it neutrally.`,
    };
  }
  return { ok: true, review };
}

export function actionableCount(review: Review): number {
  return review.findings.filter((finding) => finding.severity !== "nit").length;
}

export interface Marker {
  sha: string;
  confidence: number;
  actionable: number;
}

export function buildMarker(marker: Marker): string {
  return `<!-- local-review sha=${marker.sha} confidence=${marker.confidence} actionable=${marker.actionable} -->`;
}

export function parseMarker(body: string): Marker | undefined {
  // The script appends the real marker last; model text never contains one (comments are stripped).
  const matches = [
    ...body.matchAll(
      /<!-- local-review sha=([0-9a-f]{7,40}) confidence=([1-5]) actionable=(\d+) -->/g,
    ),
  ];
  const match = matches.at(-1);
  if (!match) return undefined;
  return { sha: match[1] ?? "", confidence: Number(match[2]), actionable: Number(match[3]) };
}

export interface SplitFindings {
  inline: Finding[];
  outside: Finding[];
}

// Only lines inside the diff's hunks can carry an inline comment.
export function splitFindings(
  findings: readonly Finding[],
  commentable: Map<string, Set<number>>,
): SplitFindings {
  const inline: Finding[] = [];
  const outside: Finding[] = [];
  for (const finding of findings) {
    (commentable.get(finding.path)?.has(finding.line) ? inline : outside).push(finding);
  }
  return { inline, outside };
}

export function inlineCommentBody(finding: Finding): string {
  const suggestion =
    finding.suggestion === undefined ? "" : `\n\n\`\`\`suggestion\n${finding.suggestion}\n\`\`\``;
  return `**${finding.severity}: ${finding.title}**\n\n${finding.body}${suggestion}`;
}

const confidenceMeaning: Record<number, string> = {
  1: "must not merge",
  2: "serious problems",
  3: "issues to fix before merging",
  4: "only nits left",
  5: "safe to merge",
};

export interface BodyInput {
  review: Review;
  sha: string;
  outside: readonly Finding[];
  threadNotes: readonly string[];
  notFixed?: number;
}

export function renderBody({ review, sha, outside, threadNotes, notFixed = 0 }: BodyInput): string {
  const counts = (severity: Severity) =>
    review.findings.filter((finding) => finding.severity === severity).length;
  const parts = [
    "## Local review",
    `**Confidence: ${review.confidence}/5** (${confidenceMeaning[review.confidence] ?? ""})`,
    [
      "| Severity | Findings |",
      "| --- | --- |",
      ...(["critical", "major", "minor", "nit"] as const).map(
        (severity) => `| ${severity} | ${counts(severity)} |`,
      ),
    ].join("\n"),
    review.summary.trim(),
  ];
  if (outside.length > 0) {
    parts.push(
      [
        "### Outside the diff",
        ...outside.map(
          (finding) =>
            `- **${finding.severity}: ${finding.title}** (\`${finding.path}:${finding.line}\`) ${finding.body}`,
        ),
      ].join("\n"),
    );
  }
  if (threadNotes.length > 0) {
    parts.push(["### Earlier threads", ...threadNotes].join("\n"));
  }
  parts.push(
    buildMarker({
      sha,
      confidence: review.confidence,
      actionable: actionableCount(review) + notFixed,
    }),
  );
  return parts.join("\n\n");
}
