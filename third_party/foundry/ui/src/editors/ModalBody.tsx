import { Link2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Choice, Field, Form } from "@/components/forms";
import { Code } from "@/components/primitives";
import { CandidateEditor } from "@/editors/CandidateEditor";
import { ExperimentDetail } from "@/editors/ExperimentDetail";
import { SessionEditor } from "@/editors/SessionEditor";
import { CaseForm, TaskEditor } from "@/editors/TaskEditor";
import type { Modal, Props } from "@/lib/types";

export function ModalBody({
  modal,
  p,
  close,
}: {
  modal: Modal;
  p: Props;
  close: () => void;
}) {
  if (modal.kind === "session")
    return (
      <SessionEditor
        key={modal.id + "-" + modal.revision}
        modal={modal}
        p={p}
      />
    );
  if (modal.kind === "task")
    return <TaskEditor key={modal.row?.id} modal={modal} p={p} />;
  if (modal.kind === "candidate")
    return <CandidateEditor key={modal.row?.id} modal={modal} p={p} />;
  if (modal.kind === "experiment")
    return <ExperimentDetail modal={modal} p={p} />;
  if (modal.kind === "case-create")
    return <CaseForm modal={modal} p={p} close={close} />;
  if (modal.kind === "source")
    return (
      <Form
        onSave={async (v) => {
          const r = await p.run(
            modal.row ? "source.relink" : "source.add",
            { path: v.path, adapter: v.adapter, ...(modal.row ? {id: modal.row.id} : {}) },
            "Fuente conectada.",
          );
          if (r) close();
        }}
      >
        {!modal.row && <Choice
          name="adapter"
          label="Formato del agente"
          options={["codex", "claude", "pi", "omp", "canonical"].map((k) => [
            k,
            k,
          ])}
        />}
        <Field
          name="path"
          label="Ruta local"
          initial={modal.row?.path || ""}
          required
          hint="Carpeta de sesiones o archivo JSONL en este equipo."
        />
        <Button type="submit">
          <Link2 />
          Conectar fuente
        </Button>
      </Form>
    );
  return <Code value={modal.row} />;
}
