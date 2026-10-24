// Pure selection of review context from the changed paths.

export interface InstructionFile {
  path: string;
  applyTo: string[];
}

export interface ScopedDoc {
  path: string;
  scope: string[];
}

export function matchesGlob(path: string, pattern: string): boolean {
  return new Bun.Glob(pattern).match(path);
}

export function matchesAny(path: string, patterns: string[]): boolean {
  return patterns.some((pattern) => matchesGlob(path, pattern));
}

// `applyTo: "a/**,b/**"` in the YAML front matter of a path-scoped instructions file.
export function parseApplyTo(content: string): string[] {
  const front = /^---\n([\s\S]*?)\n---/.exec(content)?.[1] ?? "";
  const value = /^applyTo:\s*"?(.*?)"?\s*$/m.exec(front)?.[1] ?? "";
  return value
    .split(",")
    .map((glob) => glob.trim())
    .filter((glob) => glob.length > 0);
}

// `ignorePatterns` of .github/review.json is a newline-separated glob list. Only literals, `*` and
// `**` are allowed: the review-gate workflow implements that subset, and a richer glob would let
// the reviewer skip a PR that the gate still blocks.
export function parseIgnorePatterns(ignorePatterns: string): string[] {
  const globs = ignorePatterns
    .split("\n")
    .map((glob) => glob.trim())
    .filter((glob) => glob.length > 0);
  const unsupported = globs.find((glob) => /[?[\]{}]/.test(glob));
  if (unsupported !== undefined) {
    throw new Error(`ignorePatterns supports only literals, * and **: ${unsupported}`);
  }
  return globs;
}

// Domain skills (.pi/skills/<name>/SKILL.md) that apply to the changed paths.
const skillRules: { skill: string; globs: string[] }[] = [
  {
    skill: "domain-foundry",
    globs: [
      "internal/foundry/**",
      "internal/foundryloop/**",
      "internal/memu/**",
      "third_party/foundry/**",
      "web/src/modules/conversations/**",
      "web/src/modules/foundry/**",
    ],
  },
  {
    skill: "domain-hosts-magpie",
    globs: ["internal/hosts/**", "internal/models/**", "web/src/modules/hosts/**", "web/src/modules/models/**"],
  },
  { skill: "domain-sim", globs: ["web/src/modules/sim/**", "compose.yaml"] },
  {
    skill: "domain-app-shell",
    globs: ["cmd/**", "internal/app/**", "internal/config/**", "internal/settings/**", "web/src/app/**"],
  },
];

export function skillsFor(changedPaths: string[]): string[] {
  return skillRules
    .filter((rule) => changedPaths.some((path) => matchesAny(path, rule.globs)))
    .map((rule) => rule.skill);
}

export function instructionsFor(changedPaths: string[], files: InstructionFile[]): string[] {
  return files
    .filter((file) => changedPaths.some((path) => matchesAny(path, file.applyTo)))
    .map((file) => file.path);
}

// Docs without a scope always apply; scoped ones apply when a changed path matches.
export function docsFor(changedPaths: string[], docs: ScopedDoc[]): string[] {
  return docs
    .filter(
      (doc) => doc.scope.length === 0 || changedPaths.some((path) => matchesAny(path, doc.scope)),
    )
    .map((doc) => doc.path);
}

// Paths that go into the review context: everything not ignored (Markdown included, so docs
// drift can be checked).
export function unignoredPaths(paths: string[], ignorePatterns: string[]): string[] {
  return paths.filter((path) => !matchesAny(path, ignorePatterns));
}

// Paths that make a review necessary: not ignored and not Markdown.
export function reviewablePaths(paths: string[], ignorePatterns: string[]): string[] {
  return paths.filter((path) => !path.endsWith(".md") && !matchesAny(path, ignorePatterns));
}

// Linked issue numbers from `Closes #N` / `Fixes #N` / `Resolves #N` in a PR body.
export function linkedIssues(body: string): number[] {
  const numbers = [...body.matchAll(/\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)/gi)].map(
    (match) => Number(match[1]),
  );
  return [...new Set(numbers)];
}
