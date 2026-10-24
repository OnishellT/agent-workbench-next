import { expect, test } from "bun:test";
import { rerunFailedGates } from "./rerun.ts";

function fakeGh(listOutput: string) {
  const calls: string[][] = [];
  return {
    calls,
    exec: async (cmd: string[]) => {
      calls.push(cmd);
      return cmd[2] === "list" ? listOutput : "";
    },
  };
}

test("re-runs every failed review-gate run of the head", async () => {
  const gh = fakeGh('[{"databaseId":11},{"databaseId":22}]');
  expect(await rerunFailedGates(gh.exec, "o/r", "abc")).toEqual([11, 22]);
  expect(gh.calls[0]).toContain("review-gate.yml");
  expect(gh.calls[0]).toContain("abc");
  expect(gh.calls.slice(1)).toEqual([
    ["gh", "run", "rerun", "11", "-R", "o/r"],
    ["gh", "run", "rerun", "22", "-R", "o/r"],
  ]);
});

test("does nothing when no run failed", async () => {
  const gh = fakeGh("[]");
  expect(await rerunFailedGates(gh.exec, "o/r", "abc")).toEqual([]);
  expect(gh.calls).toHaveLength(1);
});
