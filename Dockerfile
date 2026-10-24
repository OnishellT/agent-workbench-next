FROM oven/bun:1.4.2 AS web-build
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
# Entradas del aviso que sirve el producto (raíz del repositorio).
COPY NOTICE.md GO-NOTICES.txt /src/
RUN bun run build

FROM golang:1.26.8-bookworm AS binary-build
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOTOOLCHAIN=local
# Sin este argumento la imagen reporta «development», que no identifica ningún release.
ARG VERSION=development
WORKDIR /src
COPY . ./
COPY --from=web-build /src/web/dist/ ./web/dist/
RUN make binaries VERSION="${VERSION}"

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates libstdc++6 python3 python3-venv \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m venv /opt/memu \
    && /opt/memu/bin/pip install --no-cache-dir memu-cli==0.11.0b3 PyYAML==6.0.2 \
    && useradd --uid 10001 --create-home --home-dir /data --shell /usr/sbin/nologin workbench

ENV WORKBENCH_REMOTE_ACTOR_ONLY=true \
    WORKBENCH_MEMU_BIN=/opt/memu/bin/memu-codex \
    HOME=/data \
    XDG_CONFIG_HOME=/data/.config \
    XDG_DATA_HOME=/data/.local/share \
    XDG_STATE_HOME=/data/.local/state \
    XDG_CACHE_HOME=/data/.cache \
    WORKBENCH_MAGPIE_BIN=/usr/local/bin/magpie \
    MAGPIE_BUN=/usr/local/bin/bun
WORKDIR /data
VOLUME ["/data"]

COPY --from=binary-build /src/dist/workbench /usr/local/bin/workbench
COPY --from=binary-build /src/dist/magpie /usr/local/bin/magpie
COPY --from=web-build /usr/local/bin/bun /usr/local/bin/bun
# Avisos y licencias del release; SIM copia los suyos en sus propias imágenes.
COPY NOTICE.md GO-NOTICES.txt /usr/share/doc/agent-workbench/
COPY internal/foundry/LICENSE /usr/share/doc/agent-workbench/LICENSE-AGPL-3.0-only.txt
COPY third_party/magpie/LICENSE /usr/share/doc/agent-workbench/LICENSE-magpie-MIT.txt
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/workbench"]
CMD ["--listen", "127.0.0.1:18888"]
