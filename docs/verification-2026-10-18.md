# Verificación de la integración — 2026-10-18

Se usaron el árbol de trabajo actual, un estado temporal propio y la segunda PC
Linux autorizada por SSH. No se cambió ninguna URL de proveedor de Codex. La
comparación de `openai_base_url` y `model_providers.*.base_url` fue idéntica.
No se copiaron HOME completos ni se adoptaron reglas de este encargo en proyectos
reales. Los archivos de conversación sintéticos se identificaron como ensayo.

## Implementado

- Siete secciones; equipos dentro de Conexiones. Work, terminal y Uso global
  retirados de arranque, navegación y API (404). Uso sigue en Magpie original.
- Captura automática local y SSH con consentimiento, paginación, revisiones,
  citas, proyecto y cancelación por revocación/cambio de identidad. Descubrimiento
  de fuentes nativas sin sincronización manual. Un archivo incompleto no detiene
  los siguientes; conserva un error visible.
- Aprendizajes usa el CLI original memU 0.11.0b3 sobre exportaciones privadas de
  fuentes autorizadas. Procesador nativo local o SSH, sin cambiar modelo, esfuerzo
  ni proveedores. La generación legacy queda deshabilitada, incluidas sus
  propuestas automáticas de tareas; se conservan lecturas, tareas con citas y
  decisiones humanas.
- Diff y adopción explícita por proyecto, protección contra cambios concurrentes,
  conservación de reglas ajenas, recibos y rollback. La indexación explícita toma
  solo el snapshot adoptado; rollback restaura también la entrada de memoria.
- Docker incluye Python/venv, memU y Bun; Compose incluye Ollama y descarga
  `all-minilm:22m` antes de iniciar Workbench. Modelo y memoria persistentes.
  En Docker los agentes son usuarios Linux por SSH, sin HOME nativo ficticio.

## Comprobado con servicios reales

| Recorrido | Resultado observado |
| --- | --- |
| `make check`, `make binaries` | Build web/Go, tests Go, typecheck, vet y comprobación de assets/licencias Docker pasan. |
| Navegador: captura local, memU, contratos/navegación y retirada de uso | 26 pruebas pasan; captura automática desde un archivo nativo, sin pulsar Sincronizar. |
| Contratos de conversaciones/aprendizajes y Sim | 45 pasan; se corrigió después la expectativa antigua de Equipos del único fallo de esa tanda. |
| Proyectos y conexiones | Alta/persistencia/reinicio y validaciones de proyectos pasan. La tanda posterior de Atlas/Conexiones pasa 10 pruebas; otro destino exige su fixture independiente. |
| Captura SSH | Diez archivos nativos de ensayo aparecen automáticamente, superando la página de ocho, con host/proyecto correctos. Se comprobó también: revocar bloquea un append y un archivo nuevo; reautorizar crea la revisión nueva, importa el nuevo archivo y conserva sesiones únicas. |
| Helper SSH | Instalación real devuelve 202 y luego estado `ready`. El timeout escala con el tamaño de los binarios; recuperación limitada a restos propios de instalación. |
| Procesamiento memU | CLI original prepara tres trabajos; el agente nativo elegido termina los tres en 37,1 s. Decidió no crear skills con evidencia trivial. **No acredita generación de una skill nueva.** |
| Adopción e indexación | Un artefacto manual marcado como fixture pasa revisión/CAS/adopción/indexación/búsqueda con embeddings reales. No se indexan propuestas no adoptadas ni reglas humanas ajenas. Revocar la fuente bloquea revisión; rollback restaura archivo y memoria aun revocada. |
| Sim V3, API original | Cliente A crea/edita/despliega un grafo determinista; cliente B ejecuta/consulta resultado. Canvas original visible antes/después de recarga y reinicio de Sim; el mismo run y despliegue persisten y vuelven a ejecutarse. Sin clave: 401; selector inválido: 400; reintento: completado. |
| Docker | Imágenes Workbench/Sim/realtime/migraciones/cron originales construidas en la segunda PC; Python del venv importa `memu-cli==0.11.0b3`, Bun reporta 1.4.2. Ollama y el modelo arrancan en Compose y producen embeddings reales de 384 dimensiones desde el contenedor UID10001. Doce pruebas de navegador pasan contra Docker; un proyecto y el token sobreviven al reinicio. Lectura SSH del Magpie nativo: 200 con diez agentes; su UI original muestra Codex en navegador. Local sin fallback: 503. Se corrigieron el origen/listener remoto independientes del Magpie local y el PATH nativo de SSH. |
| Restore de Sim | Dump restaurado en una base nueva: coinciden los conteos de las 179 tablas públicas. El SQL necesita `search_path=public` para los triggers de pgvector; ver operations.md. |
| Ciclo de vida | Dos pruebas pasan: proyectos sobreviven al reinicio y un hijo Magpie detenido no sobrevive al cierre. El restore en frío conserva proyectos, hosts, IDs de conversaciones/tareas y el token de acceso. |

La revisión posterior de idioma/paleta pasó sus casos vigentes; las cuatro
pruebas de móvil, zoom 200%, teclado y movimiento reducido también pasan. Los
controles desplegables se abren antes de medirlos y los controles inertes detrás
de un diálogo no se confunden con acciones disponibles.

## Límites pendientes de aceptación

La suite ampliada inicial dio 63 pases y 27 fallos: expectativas de pantallas
retiradas, selectores ambiguos tras integrar Equipos en Conexiones y fixtures
no preparadas (segundo destino, TLS, pids y estado inicial vacío). No se considera
una suite completa aprobada ni se introdujeron skips para declararla verde.

V2 necesita completar el cambio de un perfil nativo de ensayo en Magpie y abrir
ese mismo agente por SSH externo. Codex 0.160.0 está instalado en `~/.local/bin`: se
corrigieron detección y ejecución SSH para respetar esa instalación aunque SSH
omita sus directorios del PATH; el sondeo de memU reconoce su venv instalado.
V4 tiene controles de revocación y
cancelación en pruebas de integración SSH; faltan los cortes/reconexiones físicos
completos en las dos PCs. V5 no acredita una skill general ni un verificador
nuevo generado y validado por el agente: el ensayo dio una decisión vacía.

V7 sigue pendiente: la prueba realizada es LAN. WireGuard existente y Sunshine
no se modificaron; no se probó un túnel entre ambas PCs ni redes externas.
V8 conserva pendientes el cambio independiente de versiones de Magpie y Sim y
su regreso con datos compatibles. Construir los pins actuales y restaurar un
dump no prueba una actualización. No se desplegó un VPS externo.

## Cierre del laboratorio

Se detuvieron y retiraron exclusivamente el proyecto Compose de ensayo y sus
volúmenes, el helper SSH propio y las conversaciones sintéticas remotas. Se
revocó la clave SSH temporal y se eliminaron sus copias, el estado/backup privado
de ensayo y los enlaces de autenticación temporales. Los datos y procesos
anteriores del usuario se conservaron; las imágenes construidas quedan en caché.

## Alta nueva de host tras corregir Magpie local

Se comprobó desde un registro vacío y un directorio nuevo de helper, contra el
Linux real `10.0.0.50`, sin reutilizar el registro de producción. Pasaron el alta,
el bloqueo inicial por huella desconocida, la confirmación de la huella previamente
verificada, la autenticación SSH, la instalación limpia de los binarios, el estado
`ready`, la lectura de agentes nativos y la apertura autenticada del panel original
con HTTP 200. Se detuvo el servidor aislado y se retiraron su estado y el helper
remoto de prueba; no se habilitó captura ni se ejecutaron inferencias.

Límite visible: guardar el host no prepara Magpie automáticamente. El recorrido
actual es añadir credenciales, confirmar huella e instalar/iniciar. La instalación
puede devolver HTTP 202 (`installed`) antes de estar lista; consultar «Estado de
Magpie» hasta `ready`. No se debe confundir registro o instalación con disponibilidad.
Esta comprobación cubre un alta nueva en ese Linux alcanzable por SSH, no cualquier
sistema operativo ni una máquina sin SSH o con dependencias de agente ausentes.
