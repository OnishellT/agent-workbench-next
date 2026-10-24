# Referencia del laboratorio de red

Observado el 2026-10-18; no es una configuracion a aplicar ni prueba del estado
actual. Los criterios de implementacion pertenecen a [plan.md](../plan.md).

| Dato | PC que simula el servidor | Segundo Linux autorizado |
| --- | --- | --- |
| Plataforma | Linux x86-64 | Ubuntu 24.04, x86-64 |
| SSH | Cliente del ensayo | `dev@10.0.0.50`, acceso por contrasena comprobado |
| Perfil NetworkManager | `wg-sunshine`, inactivo | `wg-sunshine`, activo |
| Direccion WireGuard | `10.77.30.1/32` | `10.77.31.1/32` |
| Puerto configurado | UDP 51820 | UDP 51821 |
| Peer/ruta observada | AllowedIPs `10.77.30.2/32` | Ruta WG a `10.77.31.2` |
| Cambia la ruta por defecto | No | No |
| Ruta a IP WG del otro | Router LAN | Router LAN |
| Tailscale | Detenido | Detenido |
| IPv4 forwarding | 0 | 1 |

No se acreditaron peers emparejados, endpoints o handshake entre ambas PCs.
No se leyeron claves privadas ni se modificaron conexiones, rutas o firewall.
Diez pings LAN: perdida 0%, RTT minimo/medio/maximo 1.410/2.611/5.731 ms.
Esta muestra no mide una VPN ni conectividad entre redes externas.

Una consulta posterior SSH con `BatchMode=yes` fallo por autenticacion.
No asumir que existe una clave disponible para automatizar acceso. Las
credenciales no se guardan aqui ni se recuperan cargando conversaciones antiguas.

Para SSH se usa la IP privada de la VPN; cada dispositivo no necesita una IP
publica. Un hub WireGuard externo necesita endpoint UDP accesible. Una PC
servidor dentro de esta LAN no demuestra accesibilidad desde Internet.

El diagnostico en `internal/hosts/wireguard.go` consulta interfaz/ruta/MTU:
por si solo no prueba peers, handshake, firewall ni conectividad completa.
