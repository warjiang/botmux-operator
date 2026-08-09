ARG BASE_IMAGE=node:22-bookworm-slim
FROM ${BASE_IMAGE}
ARG GEMINI_VERSION
USER root
RUN test -n "${GEMINI_VERSION}" && npm install -g "@google/gemini-cli@${GEMINI_VERSION}"
USER 1000:1000
