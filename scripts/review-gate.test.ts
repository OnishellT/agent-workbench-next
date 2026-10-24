import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

// Runs the workflow's own script with a fake `gh` that serves fixtures, so the shipped
// logic is the logic under test.
const workflow = readFileSync(
  resolve(import.meta.dir, "../.github/workflows/review-gate.yml"),
  "utf8",
);
const head = "a".repeat(40);
const stale = "b".repeat(40);

function extractScript(): string {
  const lines = workflow.split("\n");
  const start = lines.findIndex((line) => line.trim() === "run: |");
  if (start < 0) throw new Error("run block not found");
  const indent = (lines[start + 1] ?? "").search(/\S/);
  const block: string[] = [];
  for (const line of lines.slice(start + 1)) {
    if (line.trim() !== "" && line.search(/\S/) < indent) break;
    block.push(line.slice(indent));
  }
  return block.join("\n");
}

const marker = (sha: string, confidence: number, actionable: number) =>
  `<!-- local-review sha=${sha} confidence=${confidence} actionable=${actionable} -->`;
const review = (user: string, body: string, t: string) => ({
  user: { login: user },
  body,
  submitted_at: t,
});

interface Fixture {
  draft?: boolean;
  files?: { filename: string; previous_filename?: string }[];
  reviews?: ReturnType<typeof review>[];
  /** false: the base branch has no .github/review.json, so the fetch fails. */
  config?: boolean;
}

let dir: string;
beforeAll(() => {
  dir = mkdtempSync(join(tmpdir(), "review-gate-"));
  writeFileSync(join(dir, "script.sh"), extractScript());
  // Serves $FIXTURE_DIR/<last path segment>.json; `pulls/7` is served from pr.json.
  const gh = `#!/bin/sh
for arg; do :; done
case $arg in
  */files) cat "$FIXTURE_DIR/files.json" ;;
  */reviews) cat "$FIXTURE_DIR/reviews.json" ;;
  *contents/.github/review.json*) cat "$FIXTURE_DIR/review.json" ;;
  *) cat "$FIXTURE_DIR/pr.json" ;;
esac
`;
  writeFileSync(join(dir, "gh"), gh);
  chmodSync(join(dir, "gh"), 0o755);
});
afterAll(() => rmSync(dir, { recursive: true, force: true }));

function run(fixture: Fixture): { ok: boolean; output: string } {
  writeFileSync(
    join(dir, "pr.json"),
    JSON.stringify({ draft: fixture.draft ?? false, head: { sha: head }, base: { sha: stale } }),
  );
  writeFileSync(
    join(dir, "files.json"),
    JSON.stringify(fixture.files ?? [{ filename: "internal/a.go" }]),
  );
  writeFileSync(
    join(dir, "review.json"),
    JSON.stringify({ ignorePatterns: "web/bun.lock\n**/generated.d.ts\ndocs/assets/**" }),
  );
  if (fixture.config === false) rmSync(join(dir, "review.json"));
  writeFileSync(join(dir, "reviews.json"), JSON.stringify(fixture.reviews ?? []));
  const result = Bun.spawnSync(["bash", join(dir, "script.sh")], {
    env: {
      PATH: `${dir}:${process.env.PATH ?? ""}`,
      FIXTURE_DIR: dir,
      REPO: "o/r",
      PR: "7",
      REVIEWERS: "owner",
    },
  });
  return { ok: result.exitCode === 0, output: result.stdout.toString() + result.stderr.toString() };
}

describe("review gate", () => {
  test("fails a draft PR", () => {
    const result = run({ draft: true });
    expect(result.ok).toBe(false);
    expect(result.output).toContain("is a draft");
  });

  test("passes a documentation-only PR without any review", () => {
    expect(run({ files: [{ filename: "docs/a.md" }, { filename: "README.md" }] }).ok).toBe(true);
  });

  test("a file renamed into Markdown from code is not documentation-only", () => {
    const result = run({ files: [{ filename: "docs/a.md", previous_filename: "internal/a.go" }] });
    expect(result.ok).toBe(false);
  });

  test("passes a PR that only touches paths the reviewer ignores", () => {
    const files = [
      { filename: "web/bun.lock" },
      { filename: "internal/x/generated.d.ts" },
      { filename: "docs/assets/screens/a.png" },
    ];
    expect(run({ files }).ok).toBe(true);
  });

  test("an ignore glob does not leak onto similar paths", () => {
    expect(run({ files: [{ filename: "web/bun.lock.ts" }] }).ok).toBe(false);
    expect(run({ files: [{ filename: "src/docs/assets/a.png" }] }).ok).toBe(false);
    expect(run({ files: [{ filename: "docs/assets/a.png" }, { filename: "internal/a.go" }] }).ok).toBe(
      false,
    );
  });

  test("still requires a review when .github/review.json cannot be fetched", () => {
    const result = run({ config: false, files: [{ filename: "docs/assets/a.png" }] });
    expect(result.ok).toBe(false);
    expect(result.output).toContain("No local review of head");
  });

  test("fails when no review covers the head", () => {
    const result = run({ reviews: [review("owner", marker(stale, 5, 0), "2026-01-01T00:00:00Z")] });
    expect(result.ok).toBe(false);
    expect(result.output).toContain("No local review of head");
    expect(result.output).toContain("make review PR=7");
  });

  test("passes confidence 4 with no actionable findings", () => {
    expect(run({ reviews: [review("owner", marker(head, 4, 0), "2026-01-01T00:00:00Z")] }).ok).toBe(
      true,
    );
  });

  test("accepts a short sha in the marker", () => {
    expect(
      run({ reviews: [review("owner", marker(head.slice(0, 7), 5, 0), "2026-01-01T00:00:00Z")] })
        .ok,
    ).toBe(true);
  });

  test("fails on low confidence", () => {
    const result = run({ reviews: [review("owner", marker(head, 3, 0), "2026-01-01T00:00:00Z")] });
    expect(result.ok).toBe(false);
    expect(result.output).toContain("confidence 3/5");
  });

  test("fails on actionable findings", () => {
    const result = run({ reviews: [review("owner", marker(head, 5, 2), "2026-01-01T00:00:00Z")] });
    expect(result.ok).toBe(false);
    expect(result.output).toContain("2 actionable");
  });

  test("ignores a marker posted by someone else", () => {
    const result = run({
      reviews: [review("stranger", marker(head, 5, 0), "2026-01-01T00:00:00Z")],
    });
    expect(result.ok).toBe(false);
  });

  test("the newest review of the head wins", () => {
    const worse = [
      review("owner", marker(head, 5, 0), "2026-01-01T00:00:00Z"),
      review("owner", marker(head, 2, 3), "2026-01-02T00:00:00Z"),
    ];
    expect(run({ reviews: worse }).ok).toBe(false);
    expect(run({ reviews: [...worse].reverse() }).ok).toBe(false);
  });

  test("ignores reviews without a body", () => {
    const result = run({
      reviews: [{ ...review("owner", "", "2026-01-01T00:00:00Z"), body: null as never }],
    });
    expect(result.ok).toBe(false);
  });
});
