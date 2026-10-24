/** Command palette contract (UI-02). Search is accent- and case-insensitive and
 *  ranks label matches before keyword matches, keeping the declared order
 *  otherwise, so the same query always opens the same result. */

export interface CommandEntry {
  id: string;
  group: string;
  label: string;
  keywords?: string;
}

export function matchCommands(
  entries: CommandEntry[],
  query: string,
  limit = 8,
): CommandEntry[] {
  const fold = (value: string) =>
    value
      .normalize("NFD")
      .replace(/\p{Diacritic}/gu, "")
      .toLowerCase()
      .trim();
  const needle = fold(query);
  if (!needle) return entries.slice(0, limit);
  const scored: { entry: CommandEntry; score: number; index: number }[] = [];
  entries.forEach((entry, index) => {
    const label = fold(entry.label);
    const rest = fold(entry.keywords ?? "");
    const score = label.startsWith(needle)
      ? 0
      : label.includes(needle)
        ? 1
        : rest.includes(needle)
          ? 2
          : -1;
    if (score >= 0) scored.push({ entry, score, index });
  });
  scored.sort((a, b) => a.score - b.score || a.index - b.index);
  return scored.slice(0, limit).map((result) => result.entry);
}
