import { expect, test } from "bun:test";
import { commentableLines, splitDiffByFile } from "./diff.ts";

const diff = `diff --git a/src/a.ts b/src/a.ts
index 111..222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -1,4 +1,5 @@
 one
-two
+two changed
+two and a half
 three
@@ -20,2 +21,3 @@ function x() {
 twenty
+added
 end
diff --git a/old.ts b/old.ts
deleted file mode 100644
--- a/old.ts
+++ /dev/null
@@ -1,2 +0,0 @@
-gone
-gone too
diff --git a/new.ts b/new.ts
new file mode 100644
--- /dev/null
+++ b/new.ts
@@ -0,0 +1,2 @@
+first
+second
\\ No newline at end of file
`;

test("maps hunk lines of the new file, counting added and context lines only", () => {
  const lines = commentableLines(diff);
  expect([...(lines.get("src/a.ts") ?? [])]).toEqual([1, 2, 3, 4, 21, 22, 23]);
  expect([...(lines.get("new.ts") ?? [])]).toEqual([1, 2]);
});

test("a deleted file has no commentable lines", () => {
  expect(commentableLines(diff).has("old.ts")).toBe(false);
});

test("lines between hunks are not commentable", () => {
  expect(commentableLines(diff).get("src/a.ts")?.has(10)).toBe(false);
});

test("splits a diff into one chunk per file by post-image path", () => {
  expect(splitDiffByFile(diff).map((file) => file.path)).toEqual(["src/a.ts", "old.ts", "new.ts"]);
});
