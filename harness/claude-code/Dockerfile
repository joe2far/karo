FROM node:22-slim

# Install Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Install envsubst (from gettext-base) and curl for health checks
RUN apt-get update && \
    apt-get install -y --no-install-recommends gettext-base curl && \
    rm -rf /var/lib/apt/lists/*

# Create workspace directory
RUN mkdir -p /workspace

COPY bootstrap-claude-code.sh /usr/local/bin/karo-bootstrap
COPY build-task-prompt.sh /usr/local/bin/karo-build-task-prompt
COPY .mcp.json.template /etc/karo/mcp-template.json

RUN chmod +x /usr/local/bin/karo-bootstrap \
    /usr/local/bin/karo-build-task-prompt

WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/karo-bootstrap"]
