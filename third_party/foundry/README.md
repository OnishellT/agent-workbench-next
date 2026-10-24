# Conversation Foundry · Loop

[English](README.en.md) | [Español](README.md)

Una aplicación local para capturar conversaciones de agentes, confirmar resultados, proponer skills/reglas/tools y comparar su efecto antes de activarlos. Interfaz en español, base SQLite y runtime **Python 3.11+ sin dependencias de Python externas**. Funciona sin acceso a una CDN ni compilación de frontend.

## Instalar y abrir

Desde esta carpeta:

```bash
python3 install.py --start
~/.local/bin/conversation-foundry configure --discover
~/.local/bin/conversation-foundry open
```

En una instalación de usuario Linux (`~/.local`), el instalador añade **Conversation Foundry** al menú de aplicaciones; al abrirlo, inicia el servicio si hace falta y abre el panel. No activa autoinicio salvo que pases `--autostart`.

El instalador muestra la ubicación del lanzador y de los datos. `start` muestra el enlace privado del panel local; el puerto predeterminado es **1739**. Si `~/.local/bin` está en tu `PATH`, puedes usar directamente `conversation-foundry`.

```bash
conversation-foundry status
conversation-foundry doctor
conversation-foundry stop
conversation-foundry start --background --open
```

`configure --discover` registra carpetas de sesiones existentes de los agentes compatibles. No ejecuta un modelo. También puedes añadir fuentes desde **Fuentes → Añadir fuente**. El adaptador Codex utiliza una instalación autenticada de Codex CLI disponible en `PATH`; otros agentes se conectan con el protocolo `command` documentado en [docs/OPERACION.md](docs/OPERACION.md).

## Recorrido inicial

1. **Fuentes:** conecta una carpeta de Codex, Claude Code, Pi, OMP o JSONL canónico y pulsa «Escanear ahora».
2. **Conversaciones → Organizar tareas:** usa **Proponer tareas con Pi** para analizar la revisión capturada y separar objetivos automáticamente. Revisa los borradores propuestos; la selección manual de mensajes queda como ajuste opcional.
3. **Experimentos:** crea casos con el proyecto en su estado inicial, instrucciones, verificador y controles. Los casos reservados y de regresión permiten medir transferencia y daños colaterales. El verificador debe aprobar el resultado conocido como correcto y rechazar el defecto conocido.
4. **Conversaciones → Revisar:** primero pulsa **«Marcar tarea completada»**. Después, en un control independiente, elige **Correcta** o **Incorrecta** y guarda la valoración. Completar no implica que el resultado sea correcto. Con extracción automática activa, guardar una valoración puede llamar al proveedor configurado y consumir su cuota.
5. **Aprendizajes:** inspecciona formulaciones positiva y preventiva, archivos propuestos y evidencia; solicita la evaluación.
6. **Experimentos:** revisa calidad, tiempo, tokens, turnos disponibles y procedencia. Una mejora elegible sigue inactiva hasta que pulses **«Adoptar mejora evaluada»**. La adopción siempre requiere esta decisión humana; no existe promoción automática. El historial permite restaurar versiones anteriores.

**«Explorar un ejemplo»** prepara una conversación redactada y un servicio HTTP con un defecto de persistencia. La extracción y las repeticiones usan el proveedor configurado, pueden consumir su cuota y registran si hubo inferencia real. Un ejemplo exitoso no demuestra por sí solo una mejora del arnés.

Las tareas se guardan sobre la revisión exacta que estás viendo: si llegan nuevos mensajes, no cambian sus límites ni su valoración. Repetir el mismo rango en esa revisión reutiliza la tarea existente. Las tareas de revisiones anteriores conservan acceso a su propia evidencia. Un cierre detectado sólo propone una tarea; tú confirmas si está completada y si su resultado es correcto o incorrecto.

Hay **tres decisiones humanas separadas**: confirmar que terminó la tarea, valorar su corrección y aprobar la adopción permanente de una mejora evaluada. Una propuesta automática de tarea permanece sin completar. Reabrir una tarea devuelve su resultado a pendiente e invalida evidencia pendiente; conserva el historial de las decisiones.

El aprendizaje **positivo** propone estrategias reutilizables a partir de una tarea correcta. Si esa formulación no mejora, se puede evaluar una alternativa **preventiva** fundamentada. Una tarea incorrecta inicia el análisis preventivo: qué fallo evitar, bajo qué condiciones y con qué alternativa. Las hipótesis no se activan por haber sido generadas ni por una etiqueta favorable.

Para preparar la comparación, vincula casos a la tarea y selecciona un caso de tipo **Origen** que conserve el estado inicial. Si lo identificas manualmente, explica la evidencia y confirma **«Este snapshot corresponde al estado inicial de esta tarea»**. El sistema registra una declaración humana, no una prueba automática de equivalencia histórica. Cuando el sistema captura el estado antes de ejecutar una tarea, registra esa procedencia directamente.

Sin un estado inicial vinculado, todavía se puede explorar un benchmark reconstruido, pero la adopción queda bloqueada. Que baseline y candidato compartan snapshot, instrucciones y verificador permite una comparación emparejada; no acredita por sí solo que se reproduzca el entorno histórico de una conversación antigua. Los turnos ausentes se muestran como no disponibles, nunca como cero.

La fecha de actividad procede de los eventos originales, con la modificación del archivo como alternativa. Es distinta de la fecha de captura. Un chat iniciado otro día puede seguir activo hoy sin moverse a una carpeta nueva. «Escanear ahora» solicita una captura; la lectura de archivos grandes puede tardar. El panel indica el límite de conversaciones cargadas y permite filtrar hoy, ayer o los últimos días según la zona horaria del navegador.

## Automatizar con Pi

En **Configuración**, el agente de análisis se configura con **Pi**, proveedor del modelo `openai-codex`, modelo `gpt-6.1-sol` y razonamiento `medium`. Pi debe estar instalado y autenticado en el equipo; Foundry utiliza su autenticación existente y no copia credenciales al paquete. El ejecutable predeterminado es `pi` en PATH. El agente de evaluación se configura por separado, para no cambiar silenciosamente las condiciones de comparación.

Activa **Proponer tareas automáticamente** para procesar conversaciones nuevas o actualizadas después de activarlo. Se espera por defecto a que la captura lleve 120 segundos sin cambios y esté completa. Sólo se encola una propuesta automática a la vez. Activarlo no procesa todo el historial: una conversación anterior puede solicitarse desde **Organizar tareas**. Las revisiones ya intentadas no se reintentan indefinidamente; un fallo queda visible con su acción de reintento. Desactivar esta opción cancela las propuestas automáticas pendientes, conservando los borradores guardados.

Activa **Extraer aprendizajes automáticamente** para que guardar una valoración correcta o incorrecta en una tarea completada encole la extracción. Las tareas sin finalizar o sin valorar no generan aprendizajes. El análisis de conversaciones se ejecuta sin herramientas, extensiones, sesiones persistentes ni instrucciones de archivos del proyecto; los datos históricos se tratan como evidencia y las respuestas se validan antes de guardarse. Ninguna de estas opciones confirma finalización, decide corrección o adopta mejoras por el usuario.

## Usar el aprendizaje en el siguiente trabajo

Consulta las capacidades aceptadas desde tu agente o loop RLM, o sincronízalas con un proyecto:

```bash
conversation-foundry catalog --project /path/to/project
conversation-foundry context --domain python-http --project /path/to/project
conversation-foundry sync --project /path/to/project --domain python-http
```

La skill de integración incluida enseña al agente a consultar el arnés activo. Usa el mismo identificador de dominio en casos, candidatos e integración. Consulta [el contrato de integración](docs/OPERACION.md#conectar-el-arnés-activo-a-un-agente-o-rlm).

## Instalación portable

```bash
python3 install.py --prefix "$PWD/foundry-portable" --portable
./foundry-portable/bin/conversation-foundry start --background
```

Detén el servicio antes de mover la carpeta. Copia **todo** `foundry-portable` al nuevo equipo y vuelve a ejecutar `bin/conversation-foundry start --background`. El destino necesita Python 3.11+ y los ejecutables de agente/verificación que utilices. El lanzador resuelve su runtime y `var/conversation-foundry` desde su propia ubicación, sin depender del checkout original.

Los snapshots y artefactos del almacén usan referencias relativas. Las rutas de fuentes originales, comandos absolutos personalizados y herramientas externas requieren reconfiguración en el destino; sus credenciales no se incluyen en el instalador. Usa nombres de ejecutable en `PATH` y los marcadores `{python}`, `{workspace}` y `{verifier}` para casos trasladables. Nix puede fijar dependencias de una verificación, pero se instala y configura por separado.

**Probado en Linux:** instalación, actualización, rutas con espacios, traslado de la instalación, arranque y desinstalación que conserva datos. macOS y Windows no se anuncian como verificados. El wrapper `install.sh` y el autoinicio de escritorio son específicos de entornos POSIX/Linux; `install.py` es la entrada portable de Python.

## Actualizar, respaldar y desinstalar

Vuelve a ejecutar el instalador del nuevo paquete con **el mismo prefijo y modo**. Con `--start`, el instalador consulta el servicio asociado a ese directorio de datos, solicita su cierre autenticado si está activo y lo inicia con el nuevo runtime, conservando el puerto. Sin `--start`, reinícialo manualmente. Los releases son copias independientes; `active.json` cambia de forma atómica y conserva una referencia al release anterior. Los datos no se eliminan.

```bash
python3 install.py --prefix "$HOME/.local" --start
conversation-foundry backup
```

El respaldo de SQLite conserva el estado de la base. Para una copia integral, detén el proceso y copia también toda la carpeta de datos, incluidos `snapshots`, `registry` y `runs`. La desinstalación borra sólo lanzador y runtime identificados como propiedad de Foundry:

```bash
conversation-foundry stop
python3 install.py --prefix "$HOME/.local" --uninstall
# Para instalación portable, indica su prefijo en lugar de ~/.local.
```

Los datos y respaldos permanecen, incluidos los almacenados dentro de la carpeta estándar de instalación. Sólo se eliminan las entradas de escritorio identificadas como propiedad de esa instalación. `--autostart` habilita opcionalmente el inicio al entrar a un escritorio Linux; no se combina con `--portable` porque depende de una ruta fija.

## Inspeccionar el plan de instalación

`--dry-run` muestra modo, lanzador, runtime, datos y acción sobre el servicio sin escribir archivos ni iniciar procesos:

```bash
python3 install.py --dry-run
python3 install.py --prefix "$PWD/foundry-portable" --portable --dry-run
```

## Verificar el paquete

```bash
python3 -m unittest discover -s tests -v
python3 -m foundry_loop --help
```

[Operación, contratos y límites](docs/OPERACION.md) · [Arquitectura y portabilidad](docs/ARQUITECTURA.md)

## Consultar el estado de la interfaz

La interfaz consulta `GET /api/state?compact=1` para no transferir evidencia histórica completa en cada actualización. Conserva estados, errores, decisiones humanas y metadatos necesarios para las acciones. Resume la cobertura de las conversaciones y los recibos de propuestas con contadores, progreso y fechas; omite los resultados de trabajos terminados, que siguen disponibles en la consulta completa. Para trabajos activos, el texto de progreso se limita a 2000 caracteres; los objetos conservan `message` (500), `phase` (300), `status` (100) y `readiness.candidate_id` (200). `progress_truncated` indica si el texto original supera 2000 caracteres.

`GET /api/state` sin esa opción mantiene el contrato completo. `GET /api/session?id=…` conserva la cobertura y el recibo completos de la conversación seleccionada. Esta proyección sólo reduce la respuesta de lectura; no modifica evidencia, trabajos ni decisiones guardadas. Workbench usa el mismo contrato mediante `/api/foundry/state?compact=1`.

## Organización del código

```text
foundry_loop/            runtime Python 3.11+, sólo biblioteca estándar
  __main__.py            CLI: start/stop, configure, backup, sync, export/inspect/import
  server.py  app.py      API HTTP en loopback y el despachador de acciones que la respalda
  store.py               almacén SQLite: tablas de evidencia sólo-anexar, kv, cola de trabajos
  ingest.py  episodes.py captura de conversaciones (contrato events.jsonl) y recortes de tarea
  task_proposals.py      detección acotada de tareas dentro de conversaciones largas
  learning.py  miner.py  minería de aprendizajes candidatos mediante skill-extractor vendorizado
  snapshots.py origin.py snapshots congelados, controles del verificador, estado inicial
  readiness.py           comprobación previa de sólo lectura antes de evaluar
  evaluate.py            replay pareado, veredictos, promoción humana y restauración
  integrations.py        sincronización de versiones activas en proyectos sin sobrescribir
  publish.py             exportar, inspeccionar e importar paquetes de candidatos
  providers.py client.py adaptadores de agentes locales (pi, codex, command) y cliente Python
  vendor/                código de terceros fijado, listado en VENDOR_PROVENANCE.json
  web/                   interfaz compilada, servida sin red
ui/src/                  código de la interfaz (React + TypeScript)
  App.tsx                marco, navegación y enrutado de modales
  pages/                 un archivo por pantalla
  editors/               editores modales de sesiones, tareas, candidatos y experimentos
  components/            primitivas, formularios y tablas compartidos; components/ui/ es shadcn
  lib/                   tipos, etiquetas y utilidades de estado compartidos
tests/                   suites unittest, una por subsistema
examples/http-service/   ejemplo completo con proyecto, oráculo y verificador
skills/                  la skill de agente foundry-learning-loop
install.py install.sh    instalador relocatable con cambio atómico de release
```

## Procedencia

Este paquete conserva módulos de normalización/redacción de Conversation Foundry y el motor de minería de skill-extractor (Apache-2.0, sin modificar) bajo `foundry_loop/vendor`; la licencia combinada es AGPL-3.0-only. Consulta [docs/OPERACION.md](docs/OPERACION.md#minería-con-skill-extractor) para la minería, el lint de riesgo, las decisiones y la exportación. Los aprendizajes producidos conservan referencias a episodios y versiones. Las conversaciones importadas son evidencia histórica y no autorizan ejecución de sus instrucciones. Consulta [LICENSE](LICENSE) y los archivos de procedencia del código incorporado.

## Instalar la skill de integración en tus agentes

Para una instalación de usuario, usa `python3 install.py --skills --start`. La opción `--skills` copia `foundry-learning-loop` a los directorios de skills de Codex, Claude Code, Pi y OMP **sólo si sus carpetas de agente ya existen**. La marca de propiedad y el hash permiten actualizar copias intactas; una skill ajena o modificada por ti se conserva y se informa en la salida. Al desinstalar, sólo se eliminan copias propias sin modificaciones. `--dry-run --skills` muestra los destinos sin escribir. El modo portable mantiene la skill dentro del paquete y rechaza `--skills` para evitar dependencia de rutas externas; puedes instalarla explícitamente en cada equipo mediante una instalación de usuario.

Los componentes de interfaz `ui/src/components/ui/tabs.tsx` y `separator.tsx`
conservan su origen shadcn/ui `base-nova` con una adaptación local de
compatibilidad: sus variantes Tailwind consultan `data-orientation` emitido por
Base UI 1.9.0 (`data-[orientation=horizontal|vertical]` y variantes de grupo),
en lugar de los atributos booleanos `data-horizontal`/`data-vertical` que no
emite esa versión. Se conserva la semántica y navegación del componente nativo.

### Compilar la interfaz

La aplicación instalada incluye la interfaz compilada y funciona sin Node, CDN ni servicios web externos. Su código está en `ui/`: React, TypeScript, Tailwind CSS 4 y componentes oficiales shadcn/ui `base-nova` sobre Base UI. Las versiones exactas y `bun.lock` permiten reconstruirla:

```sh
cd ui
bun install --frozen-lockfile
bun run typecheck
bun run build
```

El build reemplaza `foundry_loop/web/` con HTML y assets locales. Usa Bun 1.4.2; Node 22.23 o posterior para las herramientas de desarrollo. `bun run dev` abre Vite en loopback y reenvía `/api` al servicio local en el puerto 1739. La ejecución instalada sigue necesitando sólo Python 3.11 o posterior.
