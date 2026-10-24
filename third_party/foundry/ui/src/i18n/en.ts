import { enServer } from "./en.server";
import { enUi } from "./en.ui";

export const en: Record<string, string> = { ...enServer, ...enUi };
