// Re-runs the failed review-gate runs of a head after the local review is posted. Those runs
// failed because no review existed yet, and GitHub keeps them as failed check runs on the
// head; a re-run re-reads the live PR state and turns the required check green.
export type Exec = (cmd: string[]) => Promise<string>;

export async function rerunFailedGates(exec: Exec, repo: string, sha: string): Promise<number[]> {
  const listed = await exec([
    "gh",
    "run",
    "list",
    "-R",
    repo,
    "--workflow",
    "review-gate.yml",
    "--commit",
    sha,
    "--status",
    "failure",
    "--json",
    "databaseId",
  ]);
  const ids = (JSON.parse(listed) as { databaseId: number }[]).map((run) => run.databaseId);
  for (const id of ids) await exec(["gh", "run", "rerun", String(id), "-R", repo]);
  return ids;
}
