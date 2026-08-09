ARG BASE_IMAGE=node:22-bookworm-slim
FROM ${BASE_IMAGE}
ARG CLAUDE_CODE_VERSION
USER root
RUN test -n "${CLAUDE_CODE_VERSION}" && npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}"
USER 1000:1000
