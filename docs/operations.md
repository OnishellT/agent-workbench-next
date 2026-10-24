# Referencia tecnica de operacion

Describe comandos y configuracion existentes. El alcance, orden y aceptacion
estan en [plan.md](../plan.md); leer esta referencia solo al necesitar esos datos.

## Build y pruebas existentes

- Versiones de herramientas: `go.mod`, `web/package.json` y Dockerfile.
- Toolchain local: `nix develop` (o direnv con `.envrc`) carga Go y bun desde
  `flake.nix`; ver `docs/adr/0006-nix-flake-dev-shell.md`.
- `make check`: frontend, Go tests, assets, typecheck y vet.
- `make binaries`: produce `dist/workbench` y `dist/magpie` separados.
- Flags de la app: `internal/config/config.go`; entre ellos `--state-dir`,
  `--listen`, `--public-url`, `--token-file` y `--sim-url`.
- Navegador: `web/playwright.config.ts`, `web/tests/e2e/helpers.ts` y el script
  `bun run test:e2e` de `web/package.json`. El helper admite `E2E_BASE_URL`,
  `E2E_ACCESS_TOKEN_FILE` y, cuando el spec lo necesita, `E2E_PROJECT_DIR`.
  Un contexto/estado de ensayo no usa el servicio activo del usuario.

## Topologia actual de Compose

`compose.yaml` declara Workbench, Sim, PostgreSQL/pgvector, Redis, realtime,
migraciones y embeddings locales. Sim usa sus Dockerfiles originales. Workbench y Sim usan red del
host; los listeners internos estan en loopback. Revisar el archivo actual antes
de ejecutar: no asumir que una configuracion descrita ya esta desplegada.

Los origenes se configuran mediante `WORKBENCH_PUBLIC_URL`, `SIM_PUBLIC_URL` y
`WORKBENCH_MAGPIE_ORIGIN`. El acceso remoto requiere nombres y proxy/TLS que
lleguen a esos listeners. `127.0.0.1` en otro equipo no identifica al servidor.
Sim usa `/socket.io/` hacia realtime. Los paneles Magpie usan el sitio nativo
por host, con Host/Origin y sesion propios; su UI tiene rutas API absolutas y no
se coloca bajo un prefijo arbitrario. Cookies no se aislan por puerto.

Las rutas Sim incorporadas incluyen `/api/v2/workflows` y, bajo el ID,
`state`, `deploy` y `execute`. Las API keys van en `X-API-Key`; Bearer es para
OAuth. Algunas escrituras/despliegues requieren clave personal o OAuth con
permisos, no clave de workspace. Un borrador editado no queda desplegado.
Consultar el endpoint del pin antes de usarlo; no enviar secretos al bundle JS.

La imagen corre UID10001/HOME=/data y WORKBENCH_REMOTE_ACTOR_ONLY=true:
no arranca un Magpie local con identidad del contenedor. Registra en Conexiones
el usuario Linux del host por SSH y abre su Magpie desde Configuración de agentes.
Fuera del contenedor, Magpie usa el HOME del usuario que arranca Workbench;
no modifica sus permisos y mantiene sus archivos de ejecución bajo el estado privado.
El laboratorio del segundo Linux construyó las imágenes originales de Sim y la
imagen Workbench con Docker Server 29.8.2; el resultado del ensayo se registra
por separado en `verification-2026-10-18.md`.

## Extensión para Pi

`adapters/pi` es un paquete Pi (`pi install /ruta/adapters/pi`): herramienta
`workbench_api` y comando `/workbench status|open|login`. Lee el token de
`WORKBENCH_TOKEN_FILE` (por defecto el `access.token` del estado) solo para
iniciar sesión; `WORKBENCH_URL` debe coincidir con `--public-url`. Las mutaciones
piden aprobación en la UI de Pi y se bloquean en modo headless.

## Despliegue en VPS

- Workbench solo escucha en loopback y solo confía en `X-Forwarded-Proto` de un
  proxy en loopback: el proxy TLS debe correr en la red del host. `compose.yaml`
  incluye `caddy` bajo `--profile vps` con `deploy/Caddyfile`; variables
  `WORKBENCH_DOMAIN`, `SIM_DOMAIN`, `ACME_EMAIL` en `.env`.
- Los paneles Magpie por host usan `h-<hash>.<WORKBENCH_DOMAIN>` servidos en
  `127.0.0.1:18889`: hacen falta DNS wildcard (`*.dominio`) y certificado wildcard
  (reto DNS-01, imagen Caddy con el módulo de tu proveedor en `CADDY_IMAGE`).
- Sim usa `/socket.io/` del mismo origen cuando no es loopback; el Caddyfile lo
  enruta a 3002. `NEXT_PUBLIC_SOCKET_URL` queda vacío.
- La sesión del dashboard es aleatoria y vive en memoria: reiniciar Workbench
  obliga a volver a introducir el token; el token nunca viaja en la cookie.
- Binarios y helper son amd64 (`Makefile` `GOARCH`); el helper rechaza hosts que
  no sean x86_64. Elegir VPS amd64 salvo que se compile para otra arquitectura.
- Copia de seguridad: `scripts/backup.sh [dir]` (dump de PostgreSQL + volúmenes);
  `.env` contiene la clave de cifrado de Sim y se copia por vía privada.
- Embeddings: por defecto Ollama local; `MEMU_BASE_URL`/`MEMU_EMBED_MODEL`/
  `MEMU_API_KEY` en `.env` apuntan a un endpoint externo compatible con OpenAI.

## Foundry (sección Foundry)

Foundry es la aplicación original de `third_party/foundry` (Python, solo biblioteca estándar), incrustada en el
ejecutable. Workbench la arranca en loopback al primer acceso a `/foundry/` y la detiene al cerrar; el proxy
`/foundry/*` exige la sesión del dashboard y pone el token de Foundry en el servidor (nunca llega al navegador).
Con Caddy no hace falta ninguna ruta adicional: `/foundry/` pasa por el mismo `reverse_proxy` que el dashboard.

- **Requisito:** Python 3.11 o superior en la máquina que ejecuta Workbench. La imagen Docker ya lo trae
  (`python3` de Debian bookworm). En un servidor sin imagen: `apt install python3` (Debian 12, Ubuntu 24.04) o, si
  `python3` es más antiguo, instalar `python3.11`/`python3.12`: Workbench prueba `python3`, `python3.14`…`python3.11`
  y `python` y usa el primero válido; `WORKBENCH_FOUNDRY_PYTHON` fija uno concreto. Sin Python el dashboard abre
  igual y la sección Foundry explica qué falta (HTTP 503 con el detalle).
- **Datos:** `<state-dir>/foundry-loop/data` (SQLite, instantáneas, ejecuciones) y el runtime desempaquetado en
  `<state-dir>/foundry-loop/runtime/<hash>`. Con Docker están en el volumen `workbench_data`, así que
  `scripts/backup.sh` los incluye. `WORKBENCH_FOUNDRY_DATA` (ruta absoluta) elige otro directorio, por ejemplo para
  reutilizar un almacén existente con su servicio detenido.
- **Captura:** nunca registra fuentes sola; se añaden desde la página Fuentes de Foundry. En un VPS las carpetas de
  sesiones de los agentes suelen estar en otros equipos: se importan por la captura SSH de Conexiones/Conversaciones.
- **Modelo:** Foundry usa su propio proveedor (por defecto `pi`/`codex` en PATH, configurable en su página
  Configuración). En un servidor sin esas herramientas la extracción queda como pendiente real, no simulada.
- **Recursos:** en reposo unos 35 MB de RAM además de Workbench; no hay procesos si nadie abre la sección.
- **Diagnóstico:** `<state-dir>/foundry-loop/data/service.log`. Un Foundry que sobrevive a un reinicio de Workbench
  se readopta (mismo directorio, mismo pid registrado); uno que Workbench no inició nunca se detiene.

## Estado, claves y cambios de version

El archivo `.env.example` enumera las variables de despliegue; `.env` privado no
se distribuye. Los secretos persistentes de cifrado/autenticacion se conservan
entre reinicios/actualizaciones. No rotar la clave de cifrado de Sim a ciegas.

Volumenes declarados: `workbench_data`, `memu_embeddings`, `sim_postgres` y
`sim_uploads`.
El backup de ensayo incluye el estado correspondiente y los secretos por una
via privada. Una migracion DB puede necesitar restore de datos compatibles;
volver solo al ejecutable anterior no deshace una migracion.

Procedencia/pins en [NOTICE.md](../NOTICE.md). `third_party/magpie` es un modulo
Go separado y Sim tiene su workspace. El arbol contiene parches locales de
framing/build y de integracion nativa/backup. Comparar el diff con el pin antes
de sustituir una version; no tratarlos como upstream limpio. Los Dockerfiles
Sim y las licencias originales se mantienen en su proyecto.


## memU y embeddings incluidos en el despliegue

La imagen Workbench instala `python3`, el CLI `memu-cli==0.11.0b3` en un venv
propio y Bun. Compose arranca `ollama/ollama:0.40.6` y descarga
`all-minilm:22m` con `embeddings-init` antes de Workbench. El puerto de embeddings
solo se publica en loopback; no hace falta una cuenta de pago ni modificar las
URLs o modelos de Codex. La primera instalación necesita acceso a los registros
para descargar imágenes, dependencias y el modelo.

`workbench_data` conserva SQLite, recibos y configuración; `memu_embeddings`
conserva el modelo. Ambos forman parte del respaldo. La configuración de memoria
se pasa por entorno de Compose; los agentes Linux se seleccionan por SSH desde
Conexiones, porque el contenedor no representa su HOME nativo.

Las claves SSH elegidas deben existir dentro del contenedor con permisos del
UID 10001, por ejemplo en `/data/ssh/`; no montar el HOME entero del host.
Codex/OMP y sus credenciales pertenecen al usuario Linux remoto. El contenedor
los detecta, pero no copia ni inventa sus perfiles o credenciales.

El ensayo de restore de Sim con este pin de pgvector necesita `search_path=public`
al recrear los triggers que comparan vectores. `pg_restore` directo falló al
resolver `vector = vector`. Se verificó exportando primero el SQL del dump y
cambiando solamente `SELECT pg_catalog.set_config('search_path', '', false);`
a `SELECT pg_catalog.set_config('search_path', 'public', false);` antes de cargarlo
en una base nueva con `psql -v ON_ERROR_STOP=1`. No restaurar encima de producción
sin comprobar previamente una copia.

En Docker con HTTPS, `WORKBENCH_MAGPIE_ORIGIN` define el origen base nativo
(por ejemplo `https://magpie.workbench.example.com`). El proxy debe llevar sus
subdominios `h-*.magpie.workbench.example.com` al listener `127.0.0.1:18889`
conservando Host y Origin. Este listener se abre al resolver el primer equipo;
no arranca un Magpie local. Con HTTP loopback se asigna un puerto por equipo.

Para una instalación Linux nueva: copia `.env.example` a `.env`, completa sus
secretos persistentes y URLs, y ejecuta desde la raíz del repo:

```sh
docker compose build
docker compose up -d
```

Compose espera la descarga del modelo de embeddings antes de iniciar Workbench.
No requiere instalar memU, Python, Bun ni Ollama manualmente en el VPS.

Si Workbench corre directamente bajo el usuario Linux y ese usuario ya tiene
Magpie abierto, puede reutilizarlo con `WORKBENCH_MAGPIE_ATTACH_FILE=/ruta/absoluta/magpie.json`.
El archivo debe ser regular, privado (0600), y contener:

```json
{"web_url":"http://127.0.0.1:39101","gateway_url":"http://127.0.0.1:3425","web_key":"CLAVE_WEB_EXISTENTE"}
```

Usar los puertos y la clave web de esa instancia; no una clave de proveedor.
Las dos URLs deben ser HTTP loopback con puerto explícito. Workbench verifica
la API autenticada, sirve el panel en su origen aislado y no arranca ni detiene
ese Magpie. No modifica las URLs ni modelos de los agentes. Si Magpie cambia su
clave al reiniciarse, actualizar este archivo y reiniciar Workbench.
Esta opción no convierte el navegador del cliente en un host del contenedor:
en el VPS, los equipos siguen conectándose por SSH. No exponer el Magpie privado
ni montar el HOME del usuario dentro del contenedor.

En instalaciones nativas con Python de Nix, el entorno del proceso debe encontrar
`libstdc++.so.6` para importar NumPy (dependencia de memU). Añadir el directorio de
la biblioteca del compilador instalado a `LD_LIBRARY_PATH` en el lanzador local;
comprobar `memu-codex prepare --help` antes de anunciar disponibilidad. La imagen
Docker instala `libstdc++6` y no requiere una ruta de Nix.

El alta SSH conserva la confirmación de identidad. «Confirmar e instalar Magpie»
confirma la huella observada y prepara el actor; después de una respuesta 202,
la pantalla espera su estado listo hasta 30 segundos. Un error SSH o una identidad
cambiada se muestran como error. «Instalar / iniciar» permite reintentar.

memU conserva sus trabajos originales. Si hay evidencia conversacional de varios
proyectos, Workbench agrega un último trabajo de síntesis para comparar dominios
y proponer skills generales o reglas justificadas. Distintos nombres de proyecto
no prueban generalidad. Reintentar el procesamiento no repite los trabajos que
ya finalizaron para ese lote; todos siguen sujetos a revocación de la evidencia.
