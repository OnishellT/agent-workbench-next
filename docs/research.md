# Fuentes tecnicas consultadas

Consulta del 2026-10-18. Referencias bajo demanda; no instrucciones adicionales
ni una lista de proyectos que haya que instalar. El alcance vive en [plan.md](../plan.md).

| Tema | Fuente primaria |
| --- | --- |
| WireGuard y keepalive | [Quick Start](https://www.wireguard.com/quickstart/) |
| Tailscale directo/hub/relay | [Connection types](https://tailscale.com/docs/reference/connection-types), [firewall](https://tailscale.com/docs/reference/faq/firewall-ports) |
| Sim original y Docker | [Repositorio](https://github.com/simstudioai/sim), [self-hosting](https://docs.sim.ai/platform/self-hosting/docker) |
| Crear/editar/publicar workflows | [Create](https://docs.sim.ai/api-reference/workflows/createWorkflowV2), [state](https://docs.sim.ai/api-reference/workflows/replaceWorkflowState), [deploy](https://docs.sim.ai/api-reference/workflows/deployWorkflow) |
| API y herramientas Sim | [API](https://docs.sim.ai/api-reference/getting-started), [MCP deployment](https://docs.sim.ai/workflows/deployment/mcp) |
| Magpie original | [yetone/magpie](https://github.com/yetone/magpie) |
| Extraccion de skills | [Claudeception](https://github.com/blader/Claudeception) |
| Captura y memoria contextual | [claude-mem](https://github.com/thedotmack/claude-mem) |
| Organizacion por funcionalidad | [Angular Style Guide](https://angular.dev/style-guide) |
| Ejemplos de monorepo | [NestJS workspaces](https://docs.nestjs.com/cli/monorepo), [nx-examples](https://github.com/nrwl/nx-examples) |
| Organizacion Go | [Go module layout](https://go.dev/doc/modules/layout) |

Foundry ya esta en `internal/foundry`: revisar sus fuentes actuales antes de
sustituirlo. Los proyectos de memoria/extraccion citados son referencias de la
comparacion; no se instalan ni se cargan sus skills automaticamente.
