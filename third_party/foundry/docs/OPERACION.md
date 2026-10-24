# Operación

[English](OPERACION.en.md) | [Español](OPERACION.md)

## Estados y expectativas

La captura detecta señales de finalización y propone tareas sobre una revisión completa de la conversación. **No confirma que estén terminadas.** Hay tres decisiones humanas independientes:

1. Confirmar finalización: `episode.complete` con `id` y `completed: true`. La tarea queda completada y aún sin valorar.
2. Valorar el resultado: `episode.label` con `label: correct` o `incorrect`, después de la confirmación. La segunda opción aprende qué evitar; ambas rutas requieren evaluación. `pending` conserva la finalización pero retira la valoración.
3. Aprobar incorporación: después de un experimento elegible, `candidate.promote` con `id` y `experiment_id`. Ningún ajuste habilita promoción automática.

El sistema conserva revisiones, confirmaciones y etiquetas. Reabrir con `completed: false` restablece la valoración pendiente e invalida trabajos/evidencia derivados. Una etiqueta correcta no demuestra que todos los pasos fueran útiles. La extracción produce una hipótesis con formulaciones positiva y preventiva y referencias a evidencia.

Los trabajos se encolan y se pueden cancelar. Una interrupción del proceso conserva el historial; los trabajos interrumpidos aparecen para reintento explícito. Reintentar una operación de inferencia puede consumir cuota de nuevo. El panel muestra errores y motivos de decisión, sin convertir errores de infraestructura en prueba de mejora.

La promoción contrasta la evidencia y la configuración del experimento con el estado actual. Si cambias etiquetas, casos, política o arnés, vuelve a evaluar. Las propuestas sintéticas no superan el requisito de inferencia real. Las mejoras generales necesitan variedad de dominios; repetir una única tarea no demuestra generalización.

## Configurar proveedores

El proveedor de extracción lee evidencia y devuelve una propuesta estructurada. El proveedor de repetición modifica un workspace desechable para resolver una tarea. Puedes configurar ambos de manera independiente en Configuración.

**Pi:** utiliza una instalación autenticada de Pi, normalmente con el comando `["pi"]`, proveedor del modelo `openai-codex`, modelo `gpt-6.1-sol` y razonamiento `medium`. El análisis se ejecuta en una carpeta temporal vacía, sin herramientas, extensiones, MCP, skills, archivos de contexto ni sesiones persistentes. Foundry solicita JSON y valida la respuesta localmente; no afirma que Pi imponga un esquema durante la generación. La repetición conserva las herramientas integradas y opera dentro del workspace del caso, manteniendo desactivadas las extensiones y las sesiones heredadas.

El CLI permite configurar análisis y repetición por separado, sin ejecutar inferencia:

```bash
conversation-foundry configure --provider pi --provider-id openai-codex \
  --model gpt-6.1-sol --reasoning medium --target analysis
```

`--target analysis` es el valor predeterminado y conserva la configuración de repetición. Usa `--target replay` para cambiar sólo el agente de evaluación o `--target both` para aplicar explícitamente los cambios a ambos. Al cambiar entre Pi y Codex se selecciona el ejecutable correspondiente, salvo que indiques `--agent-command`. Pi requiere un único ejecutable en ese array; al cambiar al adaptador `command`, debes proporcionar su array de argumentos explícitamente.

**Codex:** instala y autentica el CLI por los mecanismos del propio agente. Foundry no guarda claves de API. Configura el comando como una lista, normalmente `["codex"]`, y opcionalmente un modelo. El diagnóstico comprueba el ejecutable; la autenticación se confirma al realizar una llamada.

**Command:** permite un adaptador para otros agentes. Se ejecuta sin shell, con la carpeta de trabajo como directorio actual y este objeto JSON en stdin:

```json
{"schema_version":1,"prompt":"...","workspace":"...","output_schema":null}
```

Debe devolver exactamente un objeto JSON en stdout:

```json
{"text":"respuesta final","usage":{"input_tokens":120,"output_tokens":80},"turns":1,"tool_calls":3,"model":"identificador-modelo","real_inference":true}
```

Para extracción, `text` contiene JSON conforme a `output_schema`. Los logs del adaptador van a stderr. Para repetición, el adaptador debe realizar el trabajo en `workspace`. No basta con describir una solución en su respuesta.

`turns` y `tool_calls` son métricas opcionales enteras no negativas. Los turnos son turnos completados del proveedor, no llamadas a herramientas ni pasos internos de razonamiento. Las métricas omitidas aparecen como no disponibles; no se inventan ceros. El adaptador Codex suma el uso de los eventos `turn.completed` y cuenta aparte las herramientas observadas.

Activa «Adaptador command con inferencia real» únicamente cuando ese adaptador efectivamente llama al modelo. El gate requiere tanto esa configuración como `real_inference: true` en su respuesta. Esta declaración es parte del contrato del adaptador, no una atestación independiente del proveedor. Los costes monetarios no se inventan cuando el adaptador no los informa.

Los comandos son arrays de argumentos. Las cadenas de shell, pipes y expansión de variables no se interpretan automáticamente. Si necesitas un flujo con varios comandos, crea un script versionado y ejecútalo.

## Verificadores

Crea los verificadores en una carpeta externa al proyecto inicial. Foundry congela una copia y registra sus hashes. Usa comandos como:

```json
["{python}", "{verifier}/verify.py", "{workspace}"]
```

El control positivo prepara una solución conocida. El verificador debe devolver 0. El control negativo introduce un defecto y devuelve 0 si logró prepararlo; luego el mismo verificador debe devolver un código distinto de cero. Los problemas de ejecución se distinguen de fallos funcionales.

Diseña controles reales para el dominio: interfaz HTTP, persistencia tras reinicio, transacciones, salida de CLI, archivos exportados o comportamiento de navegador. El verificador debe medir el resultado observable y mantenerse independiente del agente que propone la mejora.

Los procesos se ejecutan con los permisos del usuario local. Los directorios separados y las comprobaciones hash protegen frente a cambios accidentales; no constituyen una frontera de seguridad frente a código hostil. Los servicios externos usados por un caso necesitan un entorno de pruebas explícito. Nix puede fijar herramientas y servicios de un caso cuando esté instalado.

## Acceso y diagnóstico

El servidor escucha en loopback y las llamadas al API requieren `X-Foundry-Token`. Abre el enlace de `start` u `open`; `status` informa del estado sin mostrar el token. Al abrir el panel, el token pasa del fragmento de URL a `sessionStorage` y se elimina de la dirección visible. No compartas ese enlace privado.

- Si no aparece la UI, ejecuta `conversation-foundry status` y `doctor`; comprueba el puerto.
- Si hay cero conversaciones, confirma que la fuente esté activa y pulsa «Escanear ahora». Mira su error y la cobertura de la sesión.
- Si falla extracción, comprueba ejecutable, autenticación, modelo y timeout del proveedor.
- Si falla un caso al crearlo, revisa control positivo y negativo. No desactives el verificador para hacerlo pasar.
- Si la promoción está bloqueada, lee los motivos del experimento y comprueba que la evidencia siga vigente.
- Tras trasladar un paquete, reconfigura rutas de fuentes y cualquier comando que use ubicaciones absolutas del equipo anterior.

## Conectar el arnés activo a un agente o RLM

El registro aceptado se consulta por dominio y proyecto. En un loop RLM, ejecuta al inicio de la tarea y después de una promoción:

```bash
conversation-foundry context --domain python-http --project /path/to/project
```

El adaptador del agente debe incorporar el contexto devuelto a sus instrucciones de trabajo y cargar las skills pertinentes. Sólo se consultan capacidades de la versión activa; las propuestas pendientes permanecen fuera del contexto. Si trabajas directamente en un proyecto, puedes materializar la integración con:

```bash
conversation-foundry sync --project /path/to/project --domain python-http
```

La skill de integración distribuida con Foundry indica al agente que consulte el contexto activo. El RLM conserva la responsabilidad de llamar al proveedor y ejecutar las herramientas; Foundry conserva la evidencia, evalúa comparaciones y decide qué versión está activa. El dominio es un identificador operativo, por ejemplo `python-http`: debe ser consistente entre casos, candidatos y consultas de contexto.

En Aprendizajes puedes ajustar dominio y alcance de un candidato propuesto o cuya evidencia esté desactualizada. El cambio invalida resultados anteriores; vuelve a evaluar antes de promover. Una etiqueta general no sustituye la evidencia de transferencia entre dominios.


## Minería con skill-extractor

La extracción usa el motor de [skill-extractor](https://github.com/surenode-ai/skill-extractor) (Apache-2.0, vendido sin cambios en `foundry_loop/vendor/skill_extractor`) a través de su API de biblioteca: Foundry traduce el rango de eventos de la tarea al formato canónico del extractor, el extractor reconstruye, renderiza, redacta y puntúa, y `foundry_loop/miner.py` devuelve cada skill minada al contrato de candidato validado (`learning.validate_spec`). Foundry conserva la llamada al proveedor, el presupuesto, la vinculación a la revisión, los cercos de etiqueta y la validación de esquema. El proveedor recibe el prompt del minero con un contrato adicional: sólo un objeto `{"skills": [...]}`, citas verbatim como evidencia y una formulación preventiva. Toda cita que no aparezca dentro del rango de eventos de la tarea se rechaza como evidencia fabricada o descartada; un candidato cuyas citas no resuelven no se guarda.

Cada candidato conserva `category` (technique, workflow, preference, guardrail, automation, domain), `trace_outcome`, `confidence`, `utility`, `evidence_range`, el recibo `analysis_coverage` y `risk`: los hallazgos del lint de riesgo del extractor (por ejemplo `pipe-to-shell` o `credential-access`). Un candidato con hallazgos queda bloqueado para evaluar y adoptar hasta que un humano los reconozca con `candidate.acknowledge_risk` (`id`, `acknowledged: true`, `note`). El reconocimiento se guarda fuera de la especificación, con su propio registro de auditoría.

Las decisiones humanas (`candidate.promote` con `comment` opcional y `candidate.reject` con `comment`) se guardan en la tabla `decisions`, que SQLite protege con disparadores contra modificaciones y borrados. Antes de cada minería, Foundry refleja candidatos y decisiones en `miner/candidates.jsonl` y `miner/decisions.jsonl` (los archivos de estado documentados por skill-extractor) y calcula con sus funciones el bloque de calibración y los priores. Una decisión reciente aparece en el siguiente prompt con su motivo; su influencia en las puntuaciones está acotada al 5 % por decisión y al 30 % en total. No se entrena ningún modelo ni se contacta un servicio.

La evidencia renderizada se envía en un único segmento. Si supera `max_input_chars`, el trabajo falla con un mensaje explícito y no trunca ni omite nada; los mensajes que exceden los límites por mensaje del extractor aparecen en `analysis_coverage.truncated_events`. Cobertura de entrada no equivale a comprensión semántica. Las fuentes parcialmente normalizadas no son aptas para extracción hasta resolver la cobertura. Los adjuntos son referencias inertes.

## Exportar, inspeccionar e importar

`conversation-foundry export --id CANDIDATO` genera `exports/<id>.zip` con `manifest.json`, `review.json`, `skill/<name>/SKILL.md` (formato Agent Skills) y las formulaciones. Antes de salir del almacén se aplica el paso **generalizar**: las rutas del proyecto, el directorio personal, el usuario y el host se sustituyen por `{project}`, `~`, `{user}` y `{host}`; se eliminan los campos privados del minero y del proveedor, y las citas de evidencia se conservan sólo como hashes. `review.json` enumera cada sustitución y las herramientas que la skill espera. El manifiesto declara el estado del gate: exportar no aprueba.

`conversation-foundry inspect PAQUETE` muestra el manifiesto sin instalar. `conversation-foundry import PAQUETE --project RUTA` verifica los hashes de cada archivo declarado, rechaza archivos no declarados, aplica el paso **personalizar** (sustituye los marcadores por las rutas de este equipo y lista las herramientas ausentes en `PATH`), exige `--acknowledge-risk` cuando el paquete tiene hallazgos y escribe `.agents/skills/<name>/` sin sobrescribir nunca una carpeta existente. Una skill importada es una carpeta no administrada: su estado de gate procede de otro almacén y no cuenta como adopción local. La distribución de versiones adoptadas sigue siendo `sync`, con sus carpetas administradas y el bloque marcado de AGENTS.md.

## Cliente persistente y trabajo en vivo

`conversation-foundry catalog --project /ruta/proyecto` presenta un índice breve
sin cargar todos los cuerpos. `context --domain DOMINIO --project /ruta/proyecto`
resuelve sólo capacidades aceptadas y vigentes. `conversation-foundry repl` abre
Python con el cliente `foundry` preparado. Sus métodos incluyen `state()`,
`context(domain, project)`, `call(action, **kwargs)`, `wait(job_id)`, `acall` y
`await_job`. `conversation-foundry run --project /ruta/proyecto --domain python-http
--prompt "petición"` realiza una tarea real con las capacidades activas e ingiere
el resultado para revisión. No reconstruye el estado inicial de conversaciones
históricas: los casos deben capturarse antes del trabajo o prepararse como nuevos
escenarios.

## Entorno Nix

`nix develop` proporciona Python y Git; `nix run .# -- doctor` ejecuta el paquete.
`flake.lock` fija nixpkgs. Para verificaciones con dependencias específicas usa un
argv como `["nix", "develop", "/ruta/entorno#", "--command", "python",
"{verifier}/verify.py", "{workspace}"]`, con flake propio y bloqueo versionado.
Los controles positivo/negativo se ejecutan con el mismo comando. El paquete
incluye un ejemplo real HTTP/SQLite sin dependencias externas. Nix fija software;
no reproduce por sí solo servicios externos, datos de producción ni aislamiento
frente a código hostil. Esos recursos deben definirse en el caso de cada dominio.
