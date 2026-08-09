ARG BASE_IMAGE=node:22-bookworm-slim
FROM ${BASE_IMAGE}
ARG CODEX_VERSION
USER root
RUN test -n "${CODEX_VERSION}" && npm install -g "@openai/codex@${CODEX_VERSION}"
USER 1000:1000
