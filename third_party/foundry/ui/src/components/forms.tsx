import { type FormEvent, type ReactNode, useState } from "react";
import { ArrowLeft, ArrowRight, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { type Row } from "@/api";
import { Notice } from "@/components/primitives";

export function Field({
  label,
  name,
  initial = "",
  hint,
  type = "text",
  required = false,
  children,
}: {
  label: string;
  name?: string;
  initial?: any;
  hint?: string;
  type?: string;
  required?: boolean;
  children?: ReactNode;
}) {
  const id = "field-" + (name || label.replace(/\W/g, ""));
  return (
    <div>
      <label className="form-label" htmlFor={id}>
        {label}
      </label>
      {children ||
        (type === "textarea" ? (
          <Textarea
            id={id}
            name={name}
            defaultValue={initial}
            required={required}
            aria-describedby={hint ? id + "-hint" : undefined}
            rows={4}
          />
        ) : (
          <Input
            id={id}
            name={name}
            type={type}
            step={type === "number" ? "any" : undefined}
            defaultValue={initial}
            required={required}
            aria-describedby={hint ? id + "-hint" : undefined}
          />
        ))}
      {hint && (
        <p id={id + "-hint"} className="form-help">
          {hint}
        </p>
      )}
    </div>
  );
}

export function Choice({
  label,
  name,
  initial,
  options,
  disabled = false,
  hint,
}: {
  label: string;
  name: string;
  initial?: string;
  options: [string, string][];
  disabled?: boolean;
  hint?: string;
}) {
  return (
    <Field label={label} name={name} hint={hint}>
      <select
        id={"field-" + name}
        className="native-select"
        name={name}
        defaultValue={initial}
        disabled={disabled}
        aria-describedby={hint ? "field-" + name + "-hint" : undefined}
      >
        {options.map(([value, text]) => (
          <option value={value} key={value}>
            {text}
          </option>
        ))}
      </select>
    </Field>
  );
}

export function Form({
  onSave,
  children,
  className = "field-group",
}: {
  onSave: (values: Row, form: FormData) => Promise<any>;
  children: ReactNode;
  className?: string;
}) {
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (saving) return;
    const fd = new FormData(e.currentTarget);
    setSaving(true);
    setError("");
    try {
      await onSave(Object.fromEntries(fd), fd);
    } catch (e) {
      setError(String((e as Error).message));
    } finally {
      setSaving(false);
    }
  }
  return (
    <form onSubmit={submit} className={className}>
      {error && <Notice tone="error">{error}</Notice>}
      <fieldset disabled={saving} className="contents">
        {children}
      </fieldset>
      {saving && (
        <span className="flex gap-2 text-xs muted">
          <Loader2 size={14} className="animate-spin" />
          Guardando…
        </span>
      )}
    </form>
  );
}

export function Paginator({
  page,
  setPage,
  total,
  size = 20,
}: {
  page: number;
  setPage: (n: number) => void;
  total: number;
  size?: number;
}) {
  const pages = Math.max(1, Math.ceil(total / size));
  return (
    <div className="pagination">
      <span>
        {total} resultados · página {Math.min(page + 1, pages)} de {pages}
      </span>
      <div className="flex gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Página anterior"
          disabled={!page}
          onClick={() => setPage(page - 1)}
        >
          <ArrowLeft />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Página siguiente"
          disabled={page + 1 >= pages}
          onClick={() => setPage(page + 1)}
        >
          <ArrowRight />
        </Button>
      </div>
    </div>
  );
}
