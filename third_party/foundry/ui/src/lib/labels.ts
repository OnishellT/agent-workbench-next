
export const statusNames: Record<string, string> = {
  blocked: "Preparación pendiente",
  pending: "Sin valorar",
  correct: "Correcta",
  incorrect: "Incorrecta",
  queued: "En cola",
  running: "En curso",
  succeeded: "Completado",
  completed: "Completado",
  cancelled: "Cancelado",
  failed: "Falló",
  interrupted: "Interrumpido",
  proposed: "Propuesto",
  evaluating: "En evaluación",
  evaluated: "Evaluado",
  passed: "Mejora demostrada",
  discarded: "Descartado",
  stale: "Reevaluar",
  inconclusive: "No concluyente",
  accepted: "Adoptado",
  active: "Activo",
  promoted: "Adoptado",
  unknown: "Sin cierre detectado",
  suggested: "Cierre detectado",
};

const vocabulary: Record<string, string> = {
  rule: "Regla",
  skill: "Skill",
  tool: "Herramienta",
  domain: "Dominio",
  project: "Proyecto",
  general: "General",
  origin: "Origen",
  holdout: "Transferencia",
  regression: "Regresión",
};

export const term = (value: string) => vocabulary[value] || value;
