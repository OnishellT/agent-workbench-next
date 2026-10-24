# Sustitución del procesamiento por memU

Decisión del usuario: memU. Inspección: 2026-10-18; paquete ensayado 0.11.0b3.

| Proyecto | Aporta | Límite para este producto |
| --- | --- | --- |
| [memU](https://github.com/NevaMind-AI/memU) | Adaptadores de historiales Codex/Claude/Pi y otros; prepara trabajos para que el agente busque skills existentes y cree o actualice Markdown; almacenamiento local SQLite y backend de memoria configurable. | No clasifica ni prueba por sí solo skills generales/de dominio, scripts verificadores y reglas AGENTS.md con adopción/rollback humano. El modo local documenta un dispositivo; la captura multi-host sigue a cargo del dashboard. |
| [Acontext](https://github.com/memodb-io/Acontext) | API de sesiones, extracción de tareas y learning spaces con skills y archivos de scripts. | Requiere más servicios y conservar los adaptadores de captura y el control de adopción. |
| [Hermes](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/skills.md) | Agente con creación de skills y aprendizaje desde fuentes. | Sustituye también al agente ejecutor; no es un capturador universal multi-host. |
| [Hindsight](https://github.com/vectorize-io/hindsight) | Memoria con retención, recuperación y reflexión; integraciones de agentes. | Su unidad principal es memoria; no cubre la publicación revisada de los cuatro artefactos pedidos. |

## Frontera mínima

Workbench conserva captura autorizada, citas y selección de proyecto/host/agente.
El CLI original memU prepara trabajos sobre un export privado de esas fuentes.
El agente nativo elegido procesa esos trabajos; memU almacena y recupera skills.
Los archivos resultantes son propuestas. No se considera generación verificada
un build, un mock de embeddings, una carpeta vacía ni una salida sin ejecutar.

No se instaló la skill de configuración de memU en el HOME del usuario ni se
crearon tareas programadas del editor. Se usa el CLI versionado; las credenciales
del backend de memoria se configuran fuera de Git y no se muestran en el dashboard.
