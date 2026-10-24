import { expect, test } from "bun:test";
import {
  docsFor,
  instructionsFor,
  linkedIssues,
  parseApplyTo,
  parseIgnorePatterns,
  reviewablePaths,
  unignoredFiles,
  unignoredPaths,
  skillsFor,
} from "./context.ts";

test("parses applyTo globs from front matter", () => {
  const content = '---\napplyTo: "src/a/**,src/b.ts"\n---\n# Title';
  expect(parseApplyTo(content)).toEqual(["src/a/**", "src/b.ts"]);
  expect(parseApplyTo("# no front matter")).toEqual([]);
});

test("chooses skills by changed path", () => {
  expect(skillsFor(["internal/foundry/tasks.go"])).toEqual(["domain-foundry"]);
  expect(skillsFor(["web/src/modules/hosts/HostsSection.tsx", "internal/app/session.go"])).toEqual([
    "domain-hosts-magpie",
    "domain-app-shell",
  ]);
  expect(skillsFor(["web/src/modules/sim/SimSection.tsx"])).toEqual(["domain-sim"]);
  expect(skillsFor(["README.md"])).toEqual([]);
});

test("chooses path-scoped instructions and docs", () => {
  const files = [
    { path: "domain.md", applyTo: ["src/lib/domain/**"] },
    { path: "ui.md", applyTo: ["web/src/**/*.tsx", "web/src/components/**"] },
  ];
  expect(instructionsFor(["web/src/app/App.tsx"], files)).toEqual(["ui.md"]);
  const docs = [
    { path: "docs/all.md", scope: [] },
    { path: "docs/engine.md", scope: ["engine/**"] },
  ];
  expect(docsFor(["src/x.ts"], docs)).toEqual(["docs/all.md"]);
  expect(docsFor(["engine/src/x.ts"], docs)).toEqual(["docs/all.md", "docs/engine.md"]);
});

test("drops Markdown and ignored paths from the reviewable set", () => {
  const ignore = parseIgnorePatterns("web/bun.lock\n**/generated.d.ts\ndocs/assets/**");
  expect(
    reviewablePaths(
      ["README.md", "web/bun.lock", "internal/x/generated.d.ts", "docs/assets/a.png"],
      ignore,
    ),
  ).toEqual([]);
  expect(reviewablePaths(["internal/a.go", "docs/x.md"], ignore)).toEqual(["internal/a.go"]);
});

test("keeps Markdown in the context but not ignored files", () => {
  const ignore = parseIgnorePatterns("web/bun.lock");
  expect(unignoredPaths(["docs/x.md", "web/bun.lock", "internal/a.go"], ignore)).toEqual([
    "docs/x.md",
    "internal/a.go",
  ]);
});

test("finds linked issues in the PR body", () => {
  expect(linkedIssues("Closes #97\n\nalso fixes #12 and Closes #97")).toEqual([97, 12]);
  expect(linkedIssues("no links")).toEqual([]);
});

test("ignorePatterns rejects globs the review gate does not implement", () => {
  // Keep identical to the list in scripts/review-gate.test.ts.
  for (const glob of ["a?.ts", "src/[ab].ts", "src/*.{ts,js}", "!web/bun.lock", "docs/!(a).png", "docs/a\\b.png", "docs/@(a|b).png", "docs/(a).png"]) {
    expect(() => parseIgnorePatterns(`web/bun.lock\n${glob}`)).toThrow("only literals");
  }
  expect(parseIgnorePatterns("**/*.png\nthird_party/**")).toEqual(["**/*.png", "third_party/**"]);
});

test("a rename stays in the review when either of its paths is not ignored", () => {
  const ignore = parseIgnorePatterns("docs/assets/**\nweb/bun.lock");
  const files = [
    { path: "docs/assets/a.png", previousPath: "internal/a.go" },
    { path: "internal/b.go", previousPath: "docs/assets/b.png" },
    { path: "docs/assets/c.png", previousPath: "docs/assets/d.png" },
    { path: "docs/assets/e.png" },
    { path: "internal/f.go" },
    { path: "web/bun.lock" },
  ];
  expect(unignoredFiles(files, ignore).map((file) => file.path)).toEqual([
    "docs/assets/a.png",
    "internal/b.go",
    "internal/f.go",
  ]);
});
