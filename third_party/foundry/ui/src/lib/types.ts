import { type AppState, type Row } from "@/api";

export type Modal = {
  kind: string;
  id?: string;
  row?: Row;
  revision?: string;
  task?: Row;
  candidate?: Row;
  readiness?: Row;
  split?: string;
};

export type Props = {
  data: AppState;
  run: (action: string, payload?: Row, message?: string) => Promise<Row | null>;
  open: (modal: Modal) => void;
  refresh: () => Promise<void>;
  busy: boolean;
  onOpenCaptureSettings?: () => void;
};
