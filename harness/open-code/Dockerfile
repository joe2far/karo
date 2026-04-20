# OpenCode harness for KARO
# OpenCode is a terminal-native AI coding agent from SST (Apache-2.0)
# https://github.com/sst/opencode

FROM node:22-slim

# Install OpenCode CLI (published as opencode-ai on npm)
RUN npm install -g opencode-ai

# Install envsubst (from gettext-base), curl for health checks, git for workspace ops
RUN apt-get update && \
    apt-get install -y --no-install-recommends gettext-base curl git && \
    rm -rf /var/lib/apt/lists/*

# Create workspace directory
RUN mkdir -p /workspace

COPY bootstrap-open-code.sh /usr/local/bin/karo-bootstrap
COPY build-task-prompt.sh /usr/local/bin/karo-build-task-prompt
COPY opencode.json.template /etc/karo/mcp-template.json

RUN chmod +x /usr/local/bin/karo-bootstrap \
    /usr/local/bin/karo-build-task-prompt

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/karo-bootstrap"]
