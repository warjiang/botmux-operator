FROM node:22-bookworm-slim AS builder
ARG BOTMUX_VERSION
RUN test -n "${BOTMUX_VERSION}"
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential python3 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp/package
RUN npm pack "botmux@${BOTMUX_VERSION}" \
    && mkdir -p /opt/botmux \
    && tar -xzf botmux-*.tgz --strip-components=1 -C /opt/botmux \
    && cd /opt/botmux \
    && npm install --omit=dev

FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash bubblewrap ca-certificates curl git openssh-client tini tmux \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 botmux \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash botmux \
    && mkdir -p /data/home/botmux /data/workspace /workspace \
    && chown -R 1000:1000 /data /workspace
COPY --from=builder --chown=1000:1000 /opt/botmux /opt/botmux
ENV PATH="/opt/botmux/node_modules/.bin:${PATH}"
USER 1000:1000
WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["node", "/opt/botmux/dist/index-daemon.js"]
