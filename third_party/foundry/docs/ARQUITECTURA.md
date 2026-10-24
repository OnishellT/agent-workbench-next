# Arquitectura y portabilidad

[English](ARQUITECTURA.en.md) | [Español](ARQUITECTURA.md)

```text
Adaptadores locales → eventos normalizados → revisiones SQLite
                                         ↓
                                episodios y etiquetas
                                         ↓
          skill-extractor (vendido) → candidato con evidencia y lint de riesgo
                                         ↓
                      snapshot + oráculo congelado + controles
                                         ↓
                 baseline / positivo → baseline / preventivo
                                         ↓
       gate → decisión humana → registro versionado → exportación
                                         ↓
            decisiones → priores acotados del siguiente minado
```

El controlador persistente mantiene límites, estados, presupuesto y decisiones. La UI consume un API local; no ejecuta comandos del sistema directamente. Un proveedor configurable realiza extracción y repetición. El RLM puede actuar como proveedor `command` si cumple el contrato de stdin/stdout; el scheduler duradero sigue siendo responsable de las reglas de promoción. El paquete no incorpora ni simula el runtime RLM de Prime Intellect.

## Distribución

```text
PREFIX/
  bin/conversation-foundry          lanzador relocatable
  share/conversation-foundry/
    .owned                         marca de propiedad del instalador
    active.json                    release activo y configuración de ubicación
    releases/v1-HASH/
      foundry_loop/                runtime y UI offline
      skills/ examples/ docs/
      install.py install.sh
  var/conversation-foundry/        datos, sólo en modo portable
```

Fuera del modo portable, los datos por defecto se guardan en `~/.local/share/conversation-foundry/data`. Usa `--data` para una ubicación explícita; no lo combines con `--portable`.

El instalador calcula el identificador del release a partir del contenido distribuido. Copia los archivos a un directorio temporal y después cambia el puntero activo de forma atómica. El lanzador encuentra su prefijo a partir de su propia ruta, selecciona el release y ejecuta `python -m foundry_loop`. No depende del repositorio de origen ni de un entorno virtual generado en otro equipo. Las actualizaciones conservan releases anteriores y el almacén existente; `install.py --start` reinicia de forma autenticada el servicio asociado al directorio de datos para cargar el nuevo código. Sin esa opción, el proceso se reinicia manualmente. Nunca se envían señales a un PID ni se mata el ocupante de un puerto.

## Datos que viajan y datos externos

El almacén contiene SQLite, snapshots, verificadores, candidatos, ejecuciones y versiones. Copia el almacén completo con el proceso detenido. Los snapshots y las rutas internas se resuelven desde su raíz nueva. Los archivos originales de conversaciones siguen siendo fuentes externas; el paquete no copia credenciales, instalaciones de agentes ni servicios de producción.

Los ejecutables se resuelven mediante `PATH`. `{python}` usa el intérprete del runtime actual; `{workspace}` y `{verifier}` se expanden para cada prueba. Un caso que depende de servicios, binarios o rutas externas requiere que existan también en destino. Nix permite especificar esas dependencias dentro de un caso, pero el instalador no descarga Nix automáticamente.

## Reversibilidad

Restaurar una versión cambia el arnés activo y conserva el historial. No sobrescribe archivos de configuración de agentes externos sin una acción de integración explícita. Exportar un candidato produce un paquete inspeccionable y no demuestra aprobación. La desinstalación preserva datos; para eliminar historial, el usuario administra por separado su carpeta de datos.


## Referencias del diseño

- [skill-extractor](https://github.com/surenode-ai/skill-extractor) (Apache-2.0, vendido): minería de skills, lint de riesgo y aprendizaje a partir de decisiones. [skill-forge](https://github.com/gzb1128/skill-forge) (MIT): reglas de aislamiento de pruebas emparejadas. [skill-optimizer](https://github.com/hqhq1025/skill-optimizer) (MIT): separación minar/personalizar/generalizar. Referencias: SKILL-DISCO (arXiv 2606.26669), SelfEvoSkill/PACE (arXiv 2606.14239), TRACE sobre fragilidad de verificadores (arXiv 2610.11678).
- [Prime Agent](https://github.com/PrimeIntellect-ai/prime-agent): contexto del agente y refinamiento del arnés; Foundry expone un cliente Python para conectarlo a un loop.
- [Verifiers v1](https://www.primeintellect.ai/blog/verifiers-v1): separación entre tareas, arnés y entorno de ejecución.
- [Skills de Hermes](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/developer-guide/creating-skills.md): índice breve y carga de SKILL.md, scripts y recursos cuando resultan pertinentes.
- [Nix: pruebas de integración](https://nix.dev/tutorials/nixos/integration-testing-using-virtual-machines): entornos reproducibles y comprobación de comportamiento observable.

Son referencias de arquitectura; el paquete no afirma equivalencia con esos
runtimes ni incorpora sus servicios remotos.
