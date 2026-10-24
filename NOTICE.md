# Procedencia, licencias y fuente correspondiente

Este release reúne componentes que **conservan sus avisos y licencias originales**; no les impone una licencia única nueva. Los pines y la procedencia de los proyectos de terceros viven en `third_party/<proyecto>/`, y cada dependencia conserva la suya en `go.sum` y `web/bun.lock`.

## Componentes de este release

- **Workbench (primera parte):** `cmd/`, `internal/*` salvo Foundry, `web/` y este documento. Procede del repositorio propio del autor, `https://github.com/OnishellT/agent-workbench`, según el `UPSTREAM.json` de ese árbol. Esa fuente **no impone una licencia única** al trabajo propio y esta migración todavía no declara una propia: no se inventa aquí (ver «Límites de esta verificación»).
- **Foundry (lógica portada a Go):** `internal/foundry/*` lleva `SPDX-License-Identifier: AGPL-3.0-only` y el texto íntegro está en `internal/foundry/LICENSE`, copiado del canónico `apps/foundry/LICENSE` del árbol de origen. Procede de Conversation Foundry: `apps/foundry/NOTICE` y `VENDOR_PROVENANCE.json` del árbol original, con el upstream Necmttn/ax en el commit `c2aef9b876dcd96d8115f4be4a6b3c0da6d42a77`. Dos pruebas de `internal/hosts` declaran también esa licencia.
- **Foundry (aplicación Python original):** `third_party/foundry` conserva **AGPL-3.0-only** (`LICENSE`, `NOTICE`, `VENDOR_PROVENANCE.json`) y los avisos de lo que vendoriza (skill-extractor, Apache-2.0); repositorio `https://github.com/OnishellT/foundry`, commit `0dc27d9dbd3975f4d23b0bb4b5e3e40b08439964`. El ejecutable la incluye (`third_party/foundry/embed.go`) y la ejecuta como proceso local; parches y detalles en `third_party/foundry/WORKBENCH.md`.
- **Magpie:** `third_party/magpie` conserva la licencia **MIT** y su copyright (© 2026 yetone) sin cambios; pin e integración local documentados en `third_party/magpie/WORKBENCH.md` (repositorio `https://github.com/yetone/magpie`, release v0.1.1265, commit `674b6ca2881f1289c4ccdec158762978a463320c`). Se distribuye como ejecutable propio por host.
- **SIM:** `third_party/sim` conserva **Apache-2.0** (`LICENSE`) y su `NOTICE`; pin v0.9.25 / `8df65ad39a10a6c34c61ff0674e0e8a7ad2164f0` de `https://github.com/simstudioai/sim`. Sus Dockerfiles oficiales copian `LICENSE` y `NOTICE` a `/usr/share/licenses/sim/` en cada imagen.
- **Arnés embebido en el binario:** `internal/harness/bundle` viaja dentro del ejecutable y conserva los avisos junto a su fuente (por ejemplo `plugins/ponytail/LICENSE` MIT y `skill-libraries/dfm-project-kit/THIRD_PARTY_NOTICES.md` con sus `licenses/*`).
- **Dependencias:** cada módulo de `go.mod`/`go.sum` y cada paquete de `web/bun.lock` conserva su licencia. `GO-NOTICES.txt` consolida los textos de los módulos Go que el compilador enlaza realmente en los dos binarios distribuidos (`./cmd/workbench` y `third_party/magpie`), leídos del caché instalado por `scripts/go-notices.sh`; los del paquete web se generan desde la instalación exacta del lock. Ambos forman el aviso que la aplicación sirve en `/NOTICES.txt`.

## Fuera del release

- **Orbit:** retirado y **no distribuido**. No queda módulo, UI, ruta ni setting de Orbit en el producto; sólo compatibilidad para descartar sus dos claves antiguas de ajustes y las pruebas del retiro. Sus datos históricos no se copian.
- **OMP, Codex, RTK y demás ejecutores:** no van en las imágenes; se configuran por host con sus propias condiciones y licencias.
- **Datos del operador:** cuentas, claves, proyectos, conversaciones y bases de datos privadas no se distribuyen.

## Fuente correspondiente

El ejecutable y las imágenes se construyen desde este árbol (`make build`, `Dockerfile`, `compose.yaml`). Las imágenes llevan `NOTICE.md` y `GO-NOTICES.txt` junto a los textos de licencia de Foundry y Magpie en `/usr/share/doc/agent-workbench/`, y las de SIM su `LICENSE`/`NOTICE` en `/usr/share/licenses/sim/`. La fuente correspondiente de un release publicado debe ser el árbol exacto de la revisión que lo produjo, entregado o publicado por el mismo canal que el artefacto: **hoy no existe canal ni tag de release**, así que ese ofrecimiento sigue pendiente dentro de la fase de operación del [plan vigente](plan.md) y no se declara cumplido.

## Límites de esta verificación

1. El trabajo propio de Workbench no declara licencia propia en su fuente; fijarla corresponde a quien publique, no a este documento.
2. No se han comprobado los avisos dentro de imágenes construidas en esta revisión: se inspeccionaron las recetas (`Dockerfile`, `compose.yaml`) y los documentos. Que el segundo equipo Linux tenga un motor Docker accesible no acredita el contenido de estas imágenes.
3. El pin de Magpie se conserva tal como lo documenta `third_party/magpie/WORKBENCH.md`; no se ha completado una comparación de su árbol y sus parches contra la revisión upstream fijada.
4. `GO-NOTICES.txt` cubre los módulos que enlazan los dos binarios de este árbol. No cubre dependencias del host que no se distribuyen (OMP, Codex, RTK) ni las imágenes de terceros de SIM/PostgreSQL/Redis, que traen sus propios avisos.

La ausencia de una licencia en una fuente no debe interpretarse como una concesión de derechos adicionales. Los proveedores de modelos, servicios externos, imágenes de contenedor y herramientas opcionales requieren sus propias condiciones y, cuando corresponda, autenticación del usuario.

## memU

El procesamiento de aprendizajes usa el CLI original `memu-cli==0.11.0b3`
(NevaMind-AI/memU), Apache-2.0: https://github.com/NevaMind-AI/memU.
Se instala como dependencia Python independiente; no reemplaza la licencia
de los archivos de captura y evidencia existentes. Su wheel conserva sus avisos.
