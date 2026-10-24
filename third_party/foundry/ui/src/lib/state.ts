import { type AppState, type Row } from "@/api";

export function active(data: AppState, id: string) {
  return !!data.versions
    .find((v) => v.id === data.active_version)
    ?.members?.some(
      (m: Row | string) => (typeof m === "string" ? m : m.candidate_id) === id,
    );
}
