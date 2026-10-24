# Workbench: implementar y verificar

Este archivo contiene el alcance, el reparto y la aceptacion vigentes. El encargo
es terminar el producto descrito, aprovechando el codigo actual. No reiniciar
la migracion, recuperar paridad antigua ni crear otro sistema de planificacion.

## Cambio de alcance confirmado por el usuario (2026-10-18)

El procesamiento de aprendizaje se sustituye por **memU**, elegido tras comparar
memU, Acontext, Hermes y Hindsight en sus fuentes oficiales. No ampliar el motor
Foundry ni ejecutar flujos de aprendizaje sobre este encargo. La captura,
normalización, revisiones, citas y datos existentes se conservan durante la
transición; el nombre del paquete `internal/foundry` no implica conservar el motor.

memU se integra como dependencia original independiente, pin `memu-cli==0.11.0b3`.
Exportar solo conversaciones de fuentes habilitadas a un directorio de ensayo/
procesamiento privado; nunca apuntar el lector de memU a todo el HOME. El agente
procesador usa su perfil nativo y el modelo ya seleccionado por el usuario.
Decision del usuario 2026-10-23: la seccion Aprendizajes se sustituye por Foundry
(Conversation Foundry vendorizado en `third_party/foundry`, ADR 0008); memU queda
sin UI. Antes: Aprendizajes presentaba memU; preparar/procesar no equivale a adoptar. Conservar
revisión explícita, citas y alcance por proyecto antes de instalar artefactos.
La falta de configuración de memoria/embeddings se muestra como pendiente real.

La contraseña SSH comunicada por el usuario no se guarda aquí. El laboratorio
remoto sí acepta SSH y tiene Docker; se usa una clave temporal del ensayo con
huella verificada y se retira al terminar. No tocar Sunshine para inferir una VPN.

## 1. Entrada y contexto

- Trabajar desde la raiz de este repo. Leer este archivo por secciones y `AGENTS.md`; despues,
  solo el codigo, los callers y las instrucciones locales de la tarea asignada.
- Buscar por rutas acotadas. No cargar conversaciones, otros checkouts, `work/`,
  informes de ejecuciones anteriores ni todo `third_party` o el bundle de skills.
- `docs/operations.md`, `docs/network.md` y `docs/research.md` son referencias
  tecnicas bajo demanda. No agregan requisitos ni prueban funcionalidad terminada.
- El arbol tiene cambios staged, unstaged y archivos nuevos: son parte de la base.
  Un checkout limpio de HEAD no los contiene. Preservarlos; no reset, limpieza
  masiva, staging o commits del trabajo ajeno.
- Conservar el modelo elegido por el usuario y su esfuerzo predeterminado.
  Una prueba del producto no autoriza un benchmark de modelos o un flujo de
  aprendizaje sobre este encargo.

## 2. Producto que debe quedar

Un dashboard para proyectos y agentes de dispositivos Linux. Backend propio
Go + net/http + SQLite; frontend React/TypeScript existente, compilado a JS y
organizado por dominio. Conservar el estilo actual. Un repo, con Magpie y Sim
originales como aplicaciones independientes y actualizables por separado.

| Seccion visible | Resultado |
| --- | --- |
| Conversaciones | Captura automatica de fuentes habilitadas, por proyecto/host/agente; cobertura y errores visibles. |
| Tareas | Trabajo extraido de conversaciones, citas y decisiones humanas de finalizacion y resultado. |
| Foundry | Conversation Foundry original (Python, bajo `/foundry/` tras la sesion): fuentes, tareas, aprendizajes, experimentos y adopcion explicita; reemplaza a Aprendizajes. |
| Sim | UI y motor originales del servidor; agentes de otros hosts crean y usan workflows por su API o MCP. |
| Configuracion de agentes | Selector de host y UI original completa de su Magpie, con las funciones compatibles con ese host. |
| Conexiones | Registro de dispositivos, VPN/SSH, diagnostico e instalacion de dependencias elegidas por host. Incluye Equipos. |
| Ajustes | Preferencias generales, proyectos/fuentes y diagnostico de Workbench. |

Work y su terminal propia se retiran de UI, API y arranque. Se utiliza la terminal
externa por SSH. El reparto de trabajo ocurre mediante agentes nativos y acciones
remotas de Sim; la VPN solo aporta conectividad. Los bloques de Sim no ejecutan
automaticamente en otro host sin una herramienta/accion remota configurada.
El uso de tokens pertenece a Magpie; no crear otro tracker ni otra app de uso.

No crear editores alternativos de Magpie/Sim, SSO, motor de workflows, scheduler
distribuido, microservicios, framework DI/ORM, migracion a Angular/Nest ni gestor
de actualizaciones propio. Mantener sus builds y modulos upstream separados.

Magpie debe configurar el agente/perfil que el usuario abre normalmente en ese
host. El HOME privado del antiguo Work o de un contenedor no representa al
usuario Linux. No aplicar chmod de directorios privados al HOME real ni copiar
el HOME. Verificar cambios con un perfil nativo de ensayo y conservar lo ajeno.
Las instalaciones desde Conexiones son acciones limitadas a dependencias
conocidas, con destino y resultado por host; no aceptar comandos shell arbitrarios
del navegador. Capturar solo fuentes habilitadas, no todo el sistema de archivos.

## 3. Base comprobada y trampas concretas

- Laboratorio: esta PC es el servidor de pruebas; `dev@10.0.0.50` es el otro
  Linux autorizado. Todavia no existe VPS. IPs/estado pueden cambiar: comprobarlos.
- Se verifico SSH por contrasena. La prueba posterior con `BatchMode=yes` fue
  rechazada: no asumir autenticacion por clave disponible. Usar un agente/clave
  autorizados o solicitar ese dato una vez cuando haga falta; no poner secretos
  en el plan, comandos registrados, prompts de subagentes o Git.
- WireGuard local: `wg-sunshine` inactivo, `10.77.30.1/32`; remoto: activo,
  `10.77.31.1/32`. No se comprobo tunel entre ellos. Tailscale estaba detenido.
  Una muestra LAN de 2.611 ms no compara VPN ni acredita acceso entre redes.
- `cmd/workbench/main.go` crea Work, terminal y Magpie con `agents/home` privado.
  `internal/hostagent/run.go` tambien monta Work/terminal. Retirar ambos caminos.
- El helper de host y `internal/hosts/host_agent_proxy.go` tambien sirven a
  Magpie: recortar rutas retiradas sin borrar el transporte que sigue vigente.
- La captura remota existente es manual: `POST /api/hosts/{id}/history/sync`,
  paginas de hasta 8 archivos y cursor. Falta automatizar y drenar las paginas.
  `foundry/automation.go` automatiza propuestas, no la captura SSH.
- `EnsureWorkSource` conserva historial canonico de Work; no captura los agentes
  abiertos en terminal externa. No borrarlo ni borrar evidencia historica a ciegas.
- Los tests existentes pueden exigir funciones retiradas. Cambiar esas expectativas
  segun este plan; conservar las pruebas de funciones vigentes. No resucitar Work
  para ponerlas verdes ni desactivar pruebas de errores reales.

## 4. Coordinacion y paralelo

Un agente por defecto. Si el usuario pide subagentes, el coordinador prepara
los contratos y ejecuta hasta tres encargos independientes en paralelo. Solo
el coordinador delega; los workers no crean mas agentes. Ajustar al limite real
de la herramienta. No fijar proveedores/modelos/tier/effort desde perfiles viejos.

Antes de delegar, el coordinador confirma las rutas de abajo, los contratos que
cruzan dominios, puertos/estado de prueba disponibles y cualquier dependencia
de otro encargo. No cambiar firmas compartidas unilateralmente durante la ola.

| Encargo | Archivos que puede editar | Entrega |
| --- | --- | --- |
| A: hosts y Magpie | `internal/models/**`, `internal/hosts/**` salvo los compartidos listados debajo; `web/src/modules/hosts/**`, `web/src/modules/models/**`; specs hosts/models asignadas | Actor nativo del usuario, panel por host, dependencias elegidas y captura SSH automatica/paginada con consentimiento y recuperacion. |
| B: Foundry | `internal/foundry/**`, `internal/foundryloop/**`, `third_party/foundry/**`, `web/src/modules/conversations/**`, `web/src/modules/foundry/**`; specs conversations/foundry asignadas | Captura local, proyecto/host/agente y revisiones, tareas y recorrido de propuestas/evaluacion/adopcion; conservar el importador remoto existente. |
| C: Sim | `web/src/modules/sim/**`, specs Sim asignadas; archivos puntuales de `third_party/sim/**` solo si la integracion exige un parche | UI original y recorrido API crear/editar/desplegar/ejecutar/resultado desde clientes distintos. Reutilizar rutas upstream del pin incorporado. |

Solo el coordinador edita archivos compartidos: `plan.md`, `AGENTS.md`, README/docs,
`cmd/**`, `internal/app/**`, `internal/config/**`, `internal/settings/**`,
`internal/hostagent/**`, `internal/harness/**`, `web/src/app/**`, componentes/estilos
comunes, helpers/configuracion de pruebas, Docker/Compose/Makefile y manifests/locks.
Dentro de hosts tambien reserva `module.go`, `server.go`, `hosts.go`,
`hosts_public.go` y `host_agent_proxy.go`. Cambiar otro archivo compartido requiere
reasignarlo expresamente antes de editar; dos agentes no escriben el mismo archivo.

Contrato ya existente A/B: `hosts.HistoryImporter` y sus structs en
`internal/hosts/host_history.go`, implementados por
`foundry.Module.ImportHostConversation` y conectados con `SetHistoryImporter`.
A conserva esa firma; B no edita hosts. Un cambio necesario se acuerda y lo
integra el coordinador antes de continuar los callers afectados. La automatizacion
usa el servicio existente, limita concurrencia/paginas y se cancela al revocar
una fuente, cambiar identidad o cerrar la app; no depende de un navegador abierto.

Encargo minimo que se envia a cada worker, con contexto nuevo si la herramienta
lo permite. Rellenar todos los campos antes de enviarlo:

```text
Objetivo: [resultado del encargo A/B/C y criterios V que le correspondan].
Base: [raiz del repo actual; incluir los cambios locales existentes].
Puedes editar: [rutas exactas; exclusiones y un unico dueno por spec].
Contrato: [entradas/salidas compartidas que debes conservar].
Prueba: [comando focal existente y efecto observable esperado].
No cambies archivos compartidos ni crees otros agentes. Si los necesitas,
informa al coordinador y continua con la parte independiente.
Lee solo las fuentes/instrucciones aplicables; no importes historial o planes viejos.
Devuelve: archivos cambiados, efecto, comando/resultado real y pendiente/bloqueo.
```

El coordinador transmite el encargo y las reglas aplicables; no toda la
conversacion ni secretos. Si la herramienta hereda historial, indicar la misma
frontera de lectura. Un informe de worker es una pista: revisar diff y callers
y verificar integracion antes de marcar la entrega como completada.

## 5. Orden de ejecucion

1. **Preparar:** inspeccion acotada del arbol actual y contratos anteriores.
   Distribuir los archivos/specs y preparar estado de ensayo. Pedir solo datos
   externos que falten; continuar con el trabajo independiente.
2. **Construir:** encargos A/B/C en paralelo cuando se pidio. Mientras, el
   coordinador adapta shell, Conexiones/Ajustes y retira Work/terminal de arranque,
   API y helper. Conservar el transporte Magpie, proveedores/evaluadores y datos.
3. **Integrar secuencialmente:** comprobar firmas y montar los cambios. Resolver
   primero actor Magpie/host; conectar importador/captura y Foundry; despues Sim
   y shell final. Conservar los controles de autenticacion y aislamiento por host.
4. **Verificar:** el coordinador es el unico dueno del build integrado, stack de
   ensayo y pruebas finales. Workers pueden correr tests focales con estado propio;
   no reiniciar procesos, sobrescribir dist ni compartir cookies/DB con otro agente.
5. **Cerrar:** comprobar V1-V8, corregir fallos y actualizar el estado de abajo.
   Publicar un resumen breve con evidencia y limites; no reemplazar pendientes
   por mocks, 501, skip o afirmaciones basadas en capturas sueltas.

La LAN permite desarrollar el producto mientras se prepara la VPN. El despliegue
usa la PC actual; instalar un VPS externo, publicar/mergear o migrar datos privados
de otra instalacion no forma parte de este encargo.

## 6. Criterios de aceptacion

| ID | Recorrido que hay que observar sobre la implementacion integrada |
| --- | --- |
| V1 | Solo las siete secciones definidas. Work/terminal no se montan ni ejecutan; sus API devuelven 404. Conexiones contiene los equipos. Navegacion, idioma/tema y escritorio/movil existentes funcionan. |
| V2 | Registrar host con identidad SSH verificada, detectar dependencias y ejecutar una instalacion elegida con progreso/resultado. Abrir su Magpie original, cambiar un perfil nativo de ensayo y abrir ese mismo agente/perfil por SSH externo: refleja el cambio. Otro host no cambia; host inaccesible muestra error, sin fallback local. Desde Docker se sigue accediendo al usuario del host, no a un HOME privado del contenedor. |
| V3 | Un cliente crea/edita/despliega un workflow determinista por API oficial de Sim; otro lo ejecuta y consulta resultado. Visible en UI original, persiste tras recarga/reinicio y conserva un error real con recuperacion. API o MCP basta para el primer recorrido; un iframe o ejecutar un grafo creado manualmente no lo acredita. |
| V4 | Conversaciones nuevas de agentes nativos de ambas PCs aparecen automaticamente sin pulsar Sincronizar. Probar mas de 8 archivos remotos, proyecto/host/agente correcto, recaptura sin duplicados, revision/citas estables, desconexion/reconexion y revocacion en vuelo. Fuentes no soportadas quedan visibles como tales. |
| V5 | Con tareas de ensayo y decisiones humanas separadas, producir propuestas con citas; buscar equivalentes y evaluar un verificador con control positivo y negativo. Mostrar diff e instalar solo lo adoptado; preservar reglas ajenas y comprobar rollback. Skills generales requieren evidencia de varios dominios; no fabricar generalidad ni cifras de ahorro. No adoptar reglas ni etiquetar este encargo en datos reales para hacer pasar la prueba. |
| V6 | Reiniciar el stack conserva proyectos, fuentes, tareas, workflows y configuracion. Caer Sim o un host no impide abrir el dashboard. El cierre cancela el trabajo propio y no deja procesos huerfanos ni publica resultados de una autorizacion revocada. |
| V7 | SSH a la IP VPN entre las dos PCs, handshake/trafico en ambos sentidos y recuperacion al reconectar; salida normal a Internet conservada. Medir rutas/perdida/RTT comparables, distinguiendo directo, hub y relay. Mantener Sunshine existente hasta conocer su uso. La prueba entre redes externas queda pendiente hasta disponer de ese entorno; no atribuirla a una prueba LAN. |
| V8 | Docker/Compose construyen y arrancan Workbench, Sim y sus dependencias originales. Respaldar/restaurar estado y secretos en ensayo; actualizar Magpie y Sim por separado a versiones compatibles, probando sus recorridos y regreso con datos compatibles. Mantener pins, parches necesarios y licencias, sin updater propio. |

Para V4-V5 reutilizar los lectores y el motor Foundry: Codex/Claude/Pi/OMP y
canonico son formatos existentes, no soporte universal. Una tarea puede generar
skill, herramienta o regla segun su evidencia; probar los formatos con casos
apropiados. El procesador usa el proveedor ya elegido/configurado por el usuario.
Si falta, pedir su configuracion y seguir con las otras verificaciones; no elegir
un modelo, inventar respuesta ni declarar un experimento efectivo por iniciativa propia.

## 7. Comprobaciones y evidencia

Primero tests focales de los paquetes/specs que cambiaron; al integrar, ejecutar
los comandos existentes desde la raiz. No repetir checks verdes sin cambios o
un fallo nuevo que lo justifique.

Para A existen `CGO_ENABLED=0 GOAMD64=v1 go test -tags nogui ./internal/hosts ./internal/models`;
para B, `CGO_ENABLED=0 GOAMD64=v1 go test -tags nogui ./internal/foundry`.
El spec existente de C es `web/tests/e2e/sim-release.spec.ts`: desde `web`,
`bun run test:e2e sim-release.spec.ts`, con las precondiciones de navegador
indicadas abajo y solo contra una instancia de ensayo asignada.

```sh
# Si faltan las dependencias del frontend:
cd web
bun install --frozen-lockfile
cd ..

# Checks del codigo propio y build integrado (un unico dueno):
make check
make binaries
```

`make check` construye el frontend, ejecuta Go tests, verifica assets, typecheck
y vet. `make binaries` produce Workbench y Magpie. Si cambia codigo upstream,
seguir tambien sus instrucciones/checks aplicables: la suite Go raiz no incluye
automaticamente los modulos Magpie o Sim.

Levantar el binario con `--state-dir` absoluto exclusivo del ensayo y `--listen`
en un puerto loopback libre; consultar sus opciones reales. Mantener una sola
instancia integrada. Para navegador, los helpers actuales usan `E2E_BASE_URL` y
`E2E_ACCESS_TOKEN_FILE` del ensayo; el token nunca se imprime. Si un spec requiere
proyecto, `E2E_PROJECT_DIR` apunta al proyecto de ensayo del servidor.

```sh
# Con esas variables ya configuradas para la instancia de ensayo:
cd web
bun run test:e2e
```

Adaptar los specs afectados por retiradas antes de la suite final, sin omitir
los errores de funciones vigentes. Una prueba necesita su precondicion real:
sin credenciales SSH, proveedor configurado, motor Docker o red externa, marcar
solo el recorrido afectado como bloqueado y detallar el dato que falta. Agotar
la implementacion y verificacion independientes; un bloqueo no completa el plan.

Registrar en la seccion siguiente solo estado y evidencia breve: comando o
recorrido, esperado/observado y pendiente. Logs extensos van fuera del contexto
activo. Un test HTTP, mock, captura o build no sustituye V1-V8.

## 8. Estado de implementacion

Estado al 2026-10-18: integración implementada y revisada; aceptación V1-V8
**parcial**, sin certificar recorridos no observados. Evidencia y pendientes en
[docs/verification-2026-10-18.md](docs/verification-2026-10-18.md).

- [x] Preparación y reparto paralelo solicitado: Muse A/C y Deepseek B;
  integración/revisión final del coordinador. El coordinador completó memU tras
  el handoff parcial de B.
- [ ] A integrado; helper SSH real `ready`, falta V2 completo de perfil nativo.
- [ ] B integrado con memU; captura local comprobada y adopción/índice/rollback
  reales; V5 no acredita generación de una nueva skill.
- [x] C integrado; V3 API original, canvas y persistencia del mismo workflow/run
  tras reiniciar Sim comprobados.
- [x] Retirada de Work/terminal/Uso global y siete secciones verificadas.
- [ ] V4 integrado; diez archivos SSH automáticos y recaptura comprobados;
  falta completar cortes físicos/reconexión de ambas PCs.
- [ ] V7: LAN comprobada; túnel VPN entre PCs y red externa no comprobados.
- [ ] V8: imágenes construidas, embeddings y restore de Sim comprobados;
  actualización independiente y regreso de versiones pendientes.

No usar un build, un mock ni la ausencia de errores como sustituto de V1-V8.
Los cambios y datos anteriores del árbol se preservan sin staging ni commits.
