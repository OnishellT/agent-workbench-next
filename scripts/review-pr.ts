// Local pull request reviewer: `bun run review <pr> [--dry-run]`.
// Builds curated context, runs the reviewer in a disposable worktree at the PR head, and
// posts one GitHub review with inline comments and a confidence score (CONTRIBUTING.md#review-gate).
import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { parseEnv } from "node:util";
import { dirname, join, resolve } from "node:path";
import {
  docsFor,
  instructionsFor,
  linkedIssues,
  parseApplyTo,
  parseIgnorePatterns,
  reviewablePaths,
  unignoredFiles,
  skillsFor,
} from "./review/context.ts";
import { commentableLines, splitDiffByFile } from "./review/diff.ts";
import { parsePiStream } from "./review/pi-stream.ts";
import { rerunFailedGates } from "./review/rerun.ts";
import {
  buildReviewPayload,
  dedupeFindings,
  judgeCarried,
  missingVerdicts,
  noVerdictNote,
  finalizeReview,
  planThreadActions,
  renderCarried,
  reviewGate,
  scopeFindings,
} from "./review/policy.ts";
import {
  carriedFindings,
  type CarriedFinding,
  decodeReview,
  findOutgoingLeak,
  inlineCommentBody,
  type Marker,
  parseMarker,
  renderBody,
  splitFindings,
  stripHtmlComments,
  type Review,
} from "./review/result.ts";

// Tooling configuration: the reviewer CLI and its model.
const model = process.env["REVIEW_MODEL"] ?? "opencode-go/deepseek-v4.1-flash";
const timeoutMs = Number(process.env["REVIEW_TIMEOUT_MS"] ?? 10 * 60_000);
// Dedicated read-only pi profile, rebuilt from the repo's settings on every run.
const piDir = process.env["REVIEW_PI_DIR"] ?? join(homedir(), ".pi", "reviewer");
const piMainDir = process.env["PI_MAIN_DIR"] ?? join(homedir(), ".pi", "agent");
// Prompt and profile settings come from the PR's base branch when it has them (see main), so a
// PR cannot rewrite the reviewer that judges it; the local copies are the bootstrap fallback.
let piSettingsFile = resolve(import.meta.dirname, "review", "pi-settings.json");
// The reviewer reads untrusted text and can read files, so it gets a minimal environment:
// none of the caller's secrets (the git-ignored .env is loaded into process.env by Bun).
const piEnv: Record<string, string> = {
  PATH: process.env["PATH"] ?? "",
  HOME: process.env["HOME"] ?? homedir(),
  LANG: process.env["LANG"] ?? "C.UTF-8",
  TERM: "dumb",
  PI_CODING_AGENT_DIR: piDir,
  PI_TELEMETRY: "0",
};
let promptFile = resolve(import.meta.dirname, "review", "prompt.md");
// Home of the reviewer process: an empty scratch directory, not the owner's.
let reviewerHome = tmpdir();

interface Run {
  code: number;
  out: string;
  err: string;
}

async function run(
  cmd: string[],
  opts: {
    cwd?: string;
    stdin?: string;
    timeout?: number;
    env?: Record<string, string | undefined>;
  } = {},
) {
  const proc = Bun.spawn(cmd, {
    cwd: opts.cwd,
    env: opts.env,
    stdin: opts.stdin === undefined ? "ignore" : new Blob([opts.stdin]),
    stdout: "pipe",
    stderr: "pipe",
    timeout: opts.timeout,
    killSignal: "SIGKILL",
  });
  const [out, err, code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);
  return { code, out, err } satisfies Run;
}

async function sh(cmd: string[], opts: { cwd?: string; stdin?: string } = {}) {
  const result = await run(cmd, opts);
  if (result.code !== 0) {
    throw new Error(`${cmd.slice(0, 3).join(" ")} failed (${result.code}): ${result.err.trim()}`);
  }
  return result.out;
}

const usageText = `usage: bun run review <pr> [--dry-run] [--force]

Reviews a pull request with the local reviewer and posts one review (inline comments,
summary, confidence 1-5). --dry-run prints the review instead of posting it. A head that already
has a local review is skipped, and from the fourth round on the run stops; --force overrides both.
Env: REVIEW_MODEL, REVIEW_TIMEOUT_MS, REVIEW_IDLE_MS, REVIEW_PI_DIR, PI_MAIN_DIR. See CONTRIBUTING.md#review-gate.`;

function parseArgs(argv: string[]) {
  if (argv.includes("--help") || argv.includes("-h")) {
    console.log(usageText);
    process.exit(0);
  }
  const dryRun = argv.includes("--dry-run");
  const force = argv.includes("--force");
  const positional = argv.filter((arg) => !arg.startsWith("--"));
  const pr = Number(positional[0]);
  const unknown = argv.filter(
    (arg) => arg.startsWith("--") && arg !== "--dry-run" && arg !== "--force",
  );
  if (positional.length !== 1 || !Number.isInteger(pr) || pr < 1 || unknown.length > 0) {
    throw new Error("usage: bun run review <pr> [--dry-run] [--force]");
  }
  return { pr, dryRun, force };
}

interface Thread {
  id: string;
  isResolved: boolean;
  path: string;
  line: number | null;
  firstCommentId: number;
  comments: { author: string; body: string }[];
}

interface PrData {
  repo: string;
  title: string;
  body: string;
  sha: string;
  base: string;
  issues: { number: number; body: string }[];
  threads: Thread[];
  earlierReviews: string[];
  earlierMarkers: Marker[];
  carried: CarriedFinding[];
}

const threadsQuery = `
query($owner:String!,$name:String!,$pr:Int!){repository(owner:$owner,name:$name){
pullRequest(number:$pr){reviewThreads(first:100){pageInfo{hasNextPage}nodes{id isResolved path line
comments(first:50){nodes{databaseId author{login} body}}}}}}}`;

async function fetchPr(pr: number): Promise<PrData> {
  const repo = (
    await sh(["gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"])
  ).trim();
  const [owner = "", name = ""] = repo.split("/");
  const view = JSON.parse(
    await sh([
      "gh",
      "pr",
      "view",
      String(pr),
      "-R",
      repo,
      "--json",
      "title,body,headRefOid,baseRefName,reviews",
    ]),
  ) as {
    title: string;
    body: string;
    headRefOid: string;
    baseRefName: string;
    reviews: { body: string; author: { login: string } | null }[];
  };
  const graphql = JSON.parse(
    await sh([
      "gh",
      "api",
      "graphql",
      "-F",
      `owner=${owner}`,
      "-F",
      `name=${name}`,
      "-F",
      `pr=${pr}`,
      "-f",
      `query=${threadsQuery}`,
    ]),
  ) as {
    data: {
      repository: {
        pullRequest: {
          reviewThreads: {
            pageInfo: { hasNextPage: boolean };
            nodes: {
              id: string;
              isResolved: boolean;
              path: string;
              line: number | null;
              comments: {
                nodes: { databaseId: number; author: { login: string } | null; body: string }[];
              };
            }[];
          };
        };
      };
    };
  };
  const threadData = graphql.data.repository.pullRequest.reviewThreads;
  if (threadData.pageInfo.hasNextPage)
    console.error("warning: only the first 100 review threads are included");
  const me = (await sh(["gh", "api", "user", "--jq", ".login"])).trim();
  const myReviews = view.reviews.filter((review) => review.author?.login === me);
  const issues = [];
  for (const number of linkedIssues(view.body)) {
    const issue = JSON.parse(
      await sh(["gh", "issue", "view", String(number), "-R", repo, "--json", "body"]),
    ) as { body: string };
    issues.push({ number, body: issue.body });
  }
  return {
    repo,
    title: view.title,
    body: view.body,
    sha: view.headRefOid,
    base: view.baseRefName,
    issues,
    threads: threadData.nodes.map((node) => ({
      id: node.id,
      isResolved: node.isResolved,
      firstCommentId: node.comments.nodes[0]?.databaseId ?? 0,
      path: node.path,
      line: node.line,
      comments: node.comments.nodes.map((comment) => ({
        author: comment.author?.login ?? "ghost",
        body: comment.body,
      })),
    })),
    earlierReviews: myReviews
      .map((review) => review.body)
      .filter((body) => parseMarker(body) !== undefined),
    earlierMarkers: myReviews.flatMap((review) => parseMarker(review.body) ?? []),
    carried: carriedFindings(myReviews.map((review) => review.body)),
  };
}

function fenced(label: string, content: string) {
  return `## ${label}\n\n\`\`\`\`\`\n${content.trim()}\n\`\`\`\`\`\n`;
}

function readIfPresent(root: string, path: string) {
  try {
    return readFileSync(join(root, path), "utf8");
  } catch {
    return undefined;
  }
}

// Review policy (rules, instructions, skills, specs, ignore list) is read from the base branch,
// so a pull request cannot rewrite the rules it is judged by or exclude itself from review.
interface Policy {
  read: (path: string) => string | undefined;
  list: (dir: string) => string[];
}

function basePolicy(base: string): Policy {
  const git = (...args: string[]) =>
    Bun.spawnSync(["git", ...args], { stdout: "pipe", stderr: "ignore" });
  return {
    read: (path) => {
      const result = git("show", `origin/${base}:${path}`);
      return result.exitCode === 0 ? result.stdout.toString() : undefined;
    },
    list: (dir) => {
      const result = git("ls-tree", "--name-only", `origin/${base}`, `${dir}/`);
      return result.exitCode === 0
        ? result.stdout
            .toString()
            .split("\n")
            .filter(Boolean)
            .map((path) => path.split("/").pop() ?? "")
        : [];
    },
  };
}

function buildContext(
  policy: Policy,
  data: PrData,
  diff: string,
  paths: string[],
  commits: string,
  delta?: string,
  lastReviewed?: string,
) {
  const config = JSON.parse(policy.read(".github/review.json") ?? "{}") as {
    instructions?: string;
    customContext?: { files: { path: string; scope?: string[] }[] };
  };
  const scopedInstructions = policy
    .list(".github/instructions")
    .filter((file) => file.endsWith(".instructions.md"))
    .map((file) => {
      const path = `.github/instructions/${file}`;
      return { path, applyTo: parseApplyTo(policy.read(path) ?? "") };
    });
  const docs = docsFor(
    paths,
    (config.customContext?.files ?? [])
      .filter((file) => file.path.startsWith("docs/") || file.path === "plan.md")
      .map((file) => ({ path: file.path, scope: file.scope ?? [] })),
  );
  const sections: string[] = [];
  const add = (label: string, path: string) => {
    const content = policy.read(path);
    if (content !== undefined) sections.push(fenced(`${label}: ${path}`, content));
  };
  add("Project rules", "AGENTS.md");
  add("Contributing rules", "CONTRIBUTING.md");
  add("Review instructions", ".github/copilot-instructions.md");
  for (const path of instructionsFor(paths, scopedInstructions))
    add("Path-scoped review instructions", path);
  sections.push(fenced("Extra review instructions", config.instructions ?? ""));
  for (const skill of skillsFor(paths)) add("Skill", `.pi/skills/${skill}/SKILL.md`);
  for (const path of docs) add("Spec", path);
  sections.push(fenced("Pull request title and description", `${data.title}\n\n${data.body}`));
  for (const issue of data.issues)
    sections.push(fenced(`Linked issue #${issue.number}`, issue.body));
  if (data.earlierReviews.length > 0) {
    sections.push(
      fenced(
        "Earlier local reviews of this pull request",
        stripHtmlComments(data.earlierReviews.join("\n\n---\n\n")),
      ),
    );
  }
  const threads =
    data.threads.length === 0
      ? "No earlier review threads."
      : data.threads
          .map((thread) => {
            const replies = thread.comments.map(
              (comment) => `  [${comment.author}] ${comment.body.replace(/\n/g, "\n    ")}`,
            );
            return `thread id: ${thread.id}\nstate: ${thread.isResolved ? "resolved" : "UNRESOLVED"}\nlocation: ${thread.path}:${thread.line ?? "outdated"}\n${replies.join("\n")}`;
          })
          .join("\n\n");
  sections.push(fenced("Earlier review threads (all reviewers)", threads));
  const carried = renderCarried(data.carried);
  if (carried !== undefined) {
    sections.push(fenced("Earlier findings outside the diff (carried, no thread)", carried));
  }
  sections.push(fenced("Commits of the pull request (oldest last)", commits));
  if (delta !== undefined) {
    sections.push(
      `## Review mode: FOLLOW-UP\n\nThis is a follow-up round. The last reviewed commit is ${lastReviewed}. Review the delta below; the full diff that follows is background only. Report new findings only on lines changed in the delta (a critical problem anywhere is the one exception), and judge every unresolved earlier thread.\n\n${fenced("Delta since the last reviewed commit", delta)}`,
    );
  } else {
    sections.push("## Review mode: FIRST REVIEW\n\nReview the whole diff.\n");
  }
  sections.push(
    `Changed files: ${paths.join(", ")}\nBase branch: ${data.base}. Head commit: ${data.sha}.\n\n${fenced("Diff", diff)}`,
  );
  return sections.join("\n");
}

function isSymlink(path: string) {
  try {
    return lstatSync(path).isSymbolicLink();
  } catch {
    return false;
  }
}

// Creates or refreshes the reviewer's pi profile from the repo's settings and links the
// owner's credentials into it. Fails clearly when the provider is not signed in.
async function ensurePiProfile() {
  mkdirSync(piDir, { recursive: true, mode: 0o700 });
  const settings = readFileSync(piSettingsFile, "utf8");
  const settingsPath = join(piDir, "settings.json");
  if (readIfPresent(piDir, "settings.json") !== settings) writeFileSync(settingsPath, settings);
  for (const file of ["auth.json", "models-store.json"]) {
    const source = join(piMainDir, file);
    const target = join(piDir, file);
    if (existsSync(target)) continue;
    if (isSymlink(target)) unlinkSync(target); // dangling: the main profile changed or signed out
    if (!existsSync(source)) {
      throw new Error(`${source} is missing: sign pi in to the opencode-go provider first`);
    }
    symlinkSync(source, target);
  }
  const check = await run(["pi", "auth", "check", "--provider", "opencode-go", "--model", model], {
    env: piEnv,
  });
  if (check.code !== 0 || !check.out.includes("ready")) {
    throw new Error(
      `pi is not ready for opencode-go/${model}: ${(check.out + check.err).trim().slice(0, 300)}`,
    );
  }
}

// Secret-bearing values of the owner's env files and provider credentials (configuration such
// as origins is not secret and may be quoted).
const secretKey = /secret|key|token|password|credential/i;

function localSecrets(clone: string): string[] {
  const values: string[] = [];
  const collect = (value: unknown, key: string) => {
    if (typeof value === "string") {
      if (secretKey.test(key)) values.push(value);
    } else if (value && typeof value === "object") {
      Object.entries(value).forEach(([name, inner]) =>
        collect(inner, secretKey.test(key) ? key : name),
      );
    }
  };
  for (const file of [".env", ".env.local"]) {
    const content = readIfPresent(clone, file);
    if (content !== undefined) collect(parseEnv(content), "");
  }
  const auth = readIfPresent(piMainDir, "auth.json");
  if (auth !== undefined) {
    try {
      collect(JSON.parse(auth), "");
    } catch {
      // An unreadable credentials file has nothing to leak.
    }
  }
  // The Workbench access token of this machine's default state directory.
  const state = process.env["XDG_STATE_HOME"] ?? join(homedir(), ".local", "state");
  const token = readIfPresent(join(state, "agent-workbench-next"), "access.token")?.trim();
  if (token) values.push(token);
  return values;
}

const idleMs = Number(process.env["REVIEW_IDLE_MS"] ?? 120_000);

// Runs pi while streaming its JSON events: prints one progress line per turn, and kills the
// process when no event arrives for idleMs or the total time passes timeoutMs.
async function runPi(args: string[], cwd: string): Promise<Run> {
  const proc = Bun.spawn(args, {
    cwd,
    env: { ...piEnv, HOME: reviewerHome },
    stdin: "ignore",
    stdout: "pipe",
    stderr: "pipe",
  });
  let lastEvent = Date.now();
  let killedFor = "";
  const kill = (reason: string) => {
    killedFor = reason;
    proc.kill("SIGKILL");
  };
  const total = setTimeout(() => kill(`over ${Math.round(timeoutMs / 1000)}s in total`), timeoutMs);
  const idle = setInterval(() => {
    if (Date.now() - lastEvent > idleMs) kill(`no event for ${Math.round(idleMs / 1000)}s`);
  }, 5000);
  let turns = 0;
  let out = "";
  let pending = "";
  const progress = (line: string) => {
    if (!line.startsWith("{")) return;
    try {
      const event = JSON.parse(line) as {
        type?: string;
        message?: { role?: string; content?: { type?: string; name?: string }[] };
      };
      if (event.type !== "message_end" || event.message?.role !== "assistant") return;
      turns += 1;
      const tools = (event.message.content ?? [])
        .filter((item) => item.type === "toolCall")
        .map((item) => item.name);
      console.error(`  turn ${turns}${tools.length > 0 ? `: ${tools.join(", ")}` : ": answer"}`);
    } catch {
      // Not an event line.
    }
  };
  const reading = (async () => {
    const decoder = new TextDecoder();
    for await (const chunk of proc.stdout) {
      lastEvent = Date.now();
      const text = decoder.decode(chunk, { stream: true });
      out += text;
      const lines = (pending + text).split("\n");
      pending = lines.pop() ?? "";
      lines.forEach(progress);
    }
    progress(pending);
  })();
  const [err, code] = await Promise.all([new Response(proc.stderr).text(), proc.exited, reading]);
  clearTimeout(total);
  clearInterval(idle);
  return { code: killedFor ? -1 : code, out, err: killedFor ? `killed: ${killedFor}` : err };
}

const usage = { tokens: 0, costUsd: 0 };

// One reviewer run; a timeout or an empty answer is retried once.
async function runReviewer(
  worktree: string,
  contextFile: string,
  feedback?: string,
): Promise<string> {
  const task = feedback
    ? `Your previous answer was rejected: ${feedback}\nRe-check what you need and answer again, ending with the single fenced json block.`
    : "Review this pull request as instructed. End with the single fenced json block.";
  const args = [
    "pi",
    "-p",
    "--mode",
    "json",
    "--no-session",
    "--model",
    model,
    "--no-context-files",
    "--no-mcp",
    "--append-system-prompt",
    promptFile,
    `@${contextFile}`,
    task,
  ];
  let lastProblem = "";
  for (let attempt = 1; attempt <= 2; attempt += 1) {
    const result = await runPi(args, worktree);
    const answer = parsePiStream(result.out);
    usage.tokens += answer.usage.tokens;
    usage.costUsd += answer.usage.costUsd;
    if (result.code === 0 && answer.text.trim() !== "") return answer.text;
    lastProblem =
      result.code === 0
        ? "empty answer"
        : `exit ${result.code}: ${result.err.trim().slice(0, 300) || "timed out"}`;
    console.error(
      `reviewer run ${attempt} failed (${lastProblem})${attempt === 1 ? ", retrying" : ""}`,
    );
  }
  throw new Error(`the reviewer failed twice: ${lastProblem}`);
}

async function reviewWithRetry(worktree: string, contextFile: string): Promise<Review> {
  let feedback: string | undefined;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const answer = await runReviewer(worktree, contextFile, feedback);
    const decoded = decodeReview(answer);
    if (decoded.ok) return decoded.review;
    feedback = decoded.error;
    console.error(`review result rejected (attempt ${attempt + 1}): ${feedback.slice(0, 300)}`);
  }
  throw new Error("the reviewer did not produce a valid result twice; nothing was posted");
}

const resolveMutation = `mutation($thread:ID!){resolveReviewThread(input:{threadId:$thread}){thread{id}}}`;

async function main() {
  const { pr, dryRun, force } = parseArgs(process.argv.slice(2));
  const started = Date.now();
  const data = await fetchPr(pr);
  const gate = reviewGate(data.earlierMarkers, data.sha, force || dryRun);
  if (gate.action !== "run") {
    console.error(gate.message);
    process.exit(gate.action === "skip" ? 0 : 2);
  }
  const lastReviewed = data.earlierMarkers.at(-1)?.sha;
  const gitCommon = (
    await sh(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"])
  ).trim();
  const mainClone = dirname(gitCommon);
  await sh([
    "git",
    "fetch",
    "-q",
    "origin",
    `pull/${pr}/head`,
    `+refs/heads/${data.base}:refs/remotes/origin/${data.base}`,
  ]);
  const scratch = mkdtempSync(join(tmpdir(), `review-${pr}-`));
  const worktree = join(scratch, "tree");
  reviewerHome = join(scratch, "home");
  mkdirSync(reviewerHome);
  try {
    await run(["git", "worktree", "prune"]);
    await sh(["git", "worktree", "add", "--detach", worktree, data.sha]);
    const policy = basePolicy(data.base);
    for (const [name, assign] of [
      ["prompt.md", (path: string) => (promptFile = path)],
      ["pi-settings.json", (path: string) => (piSettingsFile = path)],
    ] as const) {
      const content = policy.read(`scripts/review/${name}`);
      if (content === undefined) continue;
      const path = join(scratch, name);
      writeFileSync(path, content);
      assign(path);
    }
    const config = JSON.parse(policy.read(".github/review.json") ?? "{}") as {
      ignorePatterns?: string;
    };
    const ignore = parseIgnorePatterns(config.ignorePatterns ?? "");
    // Changed paths come from the diff: `gh pr view --json files` stops at 100 entries.
    const fullDiff = await sh(["gh", "pr", "diff", String(pr), "-R", data.repo]);
    const files = splitDiffByFile(fullDiff);
    const kept = unignoredFiles(files, ignore);
    const paths = kept.map((file) => file.path);
    // Like the review-gate workflow, a rename counts both of its paths: moving a file out of
    // Markdown or into an ignored path still needs a review (and the marker that clears the gate).
    const gated = files.flatMap((file) => [file.path, ...(file.previousPath ? [file.previousPath] : [])]);
    if (reviewablePaths(gated, ignore).length === 0) {
      console.log(`PR #${pr} changes only Markdown or ignored files: no review needed.`);
      return;
    }
    const diff = kept.map((file) => file.text).join("");
    const commits = await sh(["git", "log", "--format=%h %s", `origin/${data.base}..${data.sha}`], {
      cwd: worktree,
    });
    // Follow-up rounds judge what changed since the last reviewed commit (when it is still around).
    let delta: string | undefined;
    if (lastReviewed !== undefined && !data.sha.startsWith(lastReviewed)) {
      const known = await run(["git", "cat-file", "-e", `${lastReviewed}^{commit}`], {
        cwd: worktree,
      });
      if (known.code === 0) {
        const deltaDiff = await sh(["git", "diff", `${lastReviewed}..${data.sha}`], {
          cwd: worktree,
        });
        delta = unignoredFiles(splitDiffByFile(deltaDiff), ignore)
          .map((file) => file.text)
          .join("");
      }
    }
    const contextFile = join(scratch, "context.md");
    await Bun.write(
      contextFile,
      buildContext(policy, data, diff, paths, commits, delta, lastReviewed),
    );
    console.error(
      `reviewing #${pr} at ${data.sha.slice(0, 7)} with ${model} (${paths.length} files)...`,
    );
    await ensurePiProfile();
    const rawReview = await reviewWithRetry(worktree, contextFile);

    const threadInfos = data.threads.map((thread) => ({
      id: thread.id,
      isResolved: thread.isResolved,
      path: thread.path,
      line: thread.line,
      firstCommentId: thread.firstCommentId,
      text: thread.comments.map((comment) => comment.body).join("\n"),
    }));
    const openThreads = new Map(
      data.threads.filter((thread) => !thread.isResolved).map((thread) => [thread.id, thread]),
    );
    const verdicts = (rawReview.resolved ?? []).filter((verdict) =>
      openThreads.has(verdict.threadId),
    );
    const unanswered = missingVerdicts(threadInfos, verdicts);
    const carriedIds = new Set(data.carried.map((item) => item.id));
    const carriedJudgement = judgeCarried(
      data.carried,
      (rawReview.resolved ?? []).filter((verdict) => carriedIds.has(verdict.threadId)),
    );
    const notFixed =
      verdicts.filter((verdict) => verdict.verdict === "not_fixed").length +
      unanswered.length +
      carriedJudgement.notFixed;
    const carriedInfos = data.carried.map(({ id, finding }) => ({
      id,
      isResolved: false,
      path: finding.path,
      line: finding.line,
      firstCommentId: 0,
      text: `${finding.title}\n${finding.body}`,
    }));
    const deltaLines = delta === undefined ? undefined : commentableLines(delta);
    const review = finalizeReview(
      rawReview,
      dedupeFindings(scopeFindings(rawReview.findings, deltaLines), [...threadInfos, ...carriedInfos]),
      notFixed,
      carriedJudgement.remaining.map((item) => item.finding),
    );
    const split = splitFindings(review.findings, commentableLines(diff));
    const { inline } = split;
    const carriedOpen = carriedJudgement.remaining.map((item) => item.finding);
    const outside = [...carriedOpen, ...split.outside];
    const threadNotes = [
      ...verdicts.map((verdict) => {
        const thread = openThreads.get(verdict.threadId);
        return `- \`${thread?.path}:${thread?.line ?? "outdated"}\` ${verdict.verdict === "fixed" ? "fixed" : "not fixed"}: ${verdict.note}`;
      }),
      ...unanswered.map(noVerdictNote),
      ...carriedJudgement.notes,
    ];
    const body = renderBody({ review, sha: data.sha, outside, threadNotes, notFixed, carried: carriedOpen });
    const secrets = localSecrets(mainClone);
    const actions = planThreadActions(verdicts, threadInfos);
    const outgoing = [
      body,
      ...inline.map(inlineCommentBody),
      ...threadNotes,
      ...actions.replies.map((reply) => reply.body),
    ];
    if (findOutgoingLeak(outgoing, secrets)) {
      throw new Error("the review contains a local secret value; nothing was posted");
    }
    const payload = buildReviewPayload(data.sha, body, inline);
    if (dryRun) {
      console.log(JSON.stringify(payload, null, 2));
      console.log(`thread verdicts: ${JSON.stringify(verdicts)}`);
      console.error(
        `dry run done in ${Math.round((Date.now() - started) / 1000)}s; usage: ${usage.tokens} tokens, $${usage.costUsd.toFixed(4)}`,
      );
      return;
    }
    const posted = JSON.parse(
      await sh(["gh", "api", `repos/${data.repo}/pulls/${pr}/reviews`, "--input", "-"], {
        stdin: JSON.stringify(payload),
      }),
    ) as { html_url: string };
    // Best effort: the review is posted either way, and a failed re-run only needs a manual one. It runs before the thread
    // replies so a failing reply cannot skip it (a second run skips the already reviewed head).
    const rerun = await rerunFailedGates(sh, data.repo, data.sha).catch((cause: unknown) => {
      console.error(
        `could not re-run review-gate: ${cause instanceof Error ? cause.message : String(cause)}`,
      );
      return [];
    });
    if (rerun.length > 0) console.error(`re-ran review-gate runs: ${rerun.join(", ")}`);
    for (const reply of actions.replies) {
      await sh([
        "gh",
        "api",
        `repos/${data.repo}/pulls/${pr}/comments/${reply.commentId}/replies`,
        "-f",
        `body=${reply.body}`,
      ]);
    }
    for (const thread of actions.resolve) {
      await sh([
        "gh",
        "api",
        "graphql",
        "-f",
        `query=${resolveMutation}`,
        "-f",
        `thread=${thread}`,
      ]);
    }
    console.log(
      `${posted.html_url}\nconfidence ${review.confidence}/5, ${review.findings.length} findings (${inline.length} inline), ${Math.round((Date.now() - started) / 1000)}s; usage: ${usage.tokens} tokens, $${usage.costUsd.toFixed(4)}`,
    );
  } finally {
    await run(["git", "worktree", "remove", "--force", worktree]);
    rmSync(scratch, { recursive: true, force: true });
  }
}

await main().catch((cause: unknown) => {
  console.error(cause instanceof Error ? cause.message : String(cause));
  process.exit(1);
});
