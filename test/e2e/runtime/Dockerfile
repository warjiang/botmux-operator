FROM node:22-bookworm-slim
RUN mkdir -p /opt/botmux/dist /data/home/botmux /data/workspace /workspace \
    && chown -R 1000:1000 /opt/botmux /data /workspace
COPY --chown=1000:1000 index-daemon.js dashboard.js /opt/botmux/dist/
USER 1000:1000
