import assert from "node:assert/strict";
import { test } from "node:test";
import { getLang, hasTranslation, setLang, translate } from "../src/i18n";

// The engine without a DOM: exact strings, placeholder patterns, whitespace around JSX fragments,
// sentence-by-sentence fallback, and the Spanish pass-through.

test("Spanish is the source language: nothing is rewritten", () => {
  setLang("es");
  assert.equal(translate("Guardar tarea"), "Guardar tarea");
  assert.equal(getLang(), "es");
});

test("English: exact strings, whitespace kept, unknown text untouched", () => {
  setLang("en");
  assert.equal(translate("Guardar tarea"), "Save task");
  assert.equal(translate("  Guardar\n   tarea \n"), "  Save task \n");
  assert.equal(translate("un texto sin entrada"), "un texto sin entrada");
  assert.equal(translate("   "), "   ");
  assert.equal(translate(""), "");
});

test("JSX fragments around an expression translate one by one", () => {
  setLang("en");
  // {total} resultados · página {page} de {pages}  ->  nodes "12", " resultados · página ", "1", " de ", "3"
  assert.equal(translate(" resultados · página "), " results · page ");
  assert.equal(translate(" de "), " of ");
  assert.equal(translate(" · captura"), " · captured");
});

test("placeholder patterns capture the variable part and the most specific one wins", () => {
  setLang("en");
  assert.equal(translate("5 fuentes registradas"), "5 sources registered");
  assert.equal(translate("1 fuentes registradas"), "1 source registered");
  assert.equal(translate("Error 502"), "Error 502");
  assert.equal(translate("Límite inválido: max_calls"), "Invalid limit: max_calls");
  // A captured part that is itself a known string is translated too.
  assert.equal(translate("Preparación pendiente: Sin valorar"), "Preparation pending: Not rated");
});

test("a message of several sentences translates the known ones and keeps the rest", () => {
  setLang("en");
  assert.equal(
    translate("Confirma la finalización de la tarea antes de evaluar. Marca la tarea como correcta o incorrecta."),
    "Confirm the task's completion before evaluating. Mark the task as correct or incorrect.",
  );
  assert.equal(
    translate("Marca la tarea como correcta o incorrecta. Algo que nadie tradujo."),
    "Mark the task as correct or incorrect. Algo que nadie tradujo.",
  );
  assert.equal(translate("Algo que nadie tradujo. Otra frase."), "Algo que nadie tradujo. Otra frase.");
});

test("composite server messages match as a whole", () => {
  setLang("en");
  assert.equal(
    translate("La primera comparación requiere 12 llamadas; max_calls permite 6. Ajusta el presupuesto."),
    "The first comparison needs 12 calls; max_calls allows 6. Adjust the budget.",
  );
  assert.equal(
    translate("El lint de riesgo marcó este candidato (shell, rm); un humano debe reconocer los hallazgos antes de adoptarlo."),
    "The risk lint flagged this candidate (shell, rm); a human must acknowledge the findings before adopting it.",
  );
  assert.ok(hasTranslation("Falta un archivo declarado: skill/x/SKILL.md"));
});

test("switching back restores Spanish", () => {
  setLang("en");
  assert.equal(translate("Cancelar"), "Cancel");
  setLang("es");
  assert.equal(translate("Cancelar"), "Cancelar");
});
