# The agent-runtime image: runs ONE agent via the shared karo-runtime library
# (PRD-KARO-v2.md §4.1, §12). The SDK adapter is the cluster-capable harness.
FROM python:3.12-slim

WORKDIR /app

# git + ssh client + CA roots so the clone-repos init-container can materialize
# spec.resources.repos into the shared workspace and the agent's open-pr skill can
# `git push` a feature branch, authenticating as the team's git service account
# (operator wires runtime.secrets["git"]; docs/DEV-WORKFLOW.md §5b). To open the
# PR itself, layer `gh` into a derived image or use a GitHub MCP server — the
# open-pr skill degrades gracefully when `gh` is absent.
RUN apt-get update \
    && apt-get install -y --no-install-recommends git openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install the shared runtime (same code the CLI uses) with the default cluster
# backend extra (Postgres — tasks/memory/mailbox) + the Claude Agent SDK. The
# base package imports without these; Postgres is the single stateful dependency
# (v2 §4.1/§4.2). Redis is optional and can be added with the [redis] extra.
COPY karo-runtime/ /app/karo-runtime/
RUN pip install --no-cache-dir "/app/karo-runtime/[sdk,postgres]"

COPY agent-runtime-image/entrypoint.py /app/entrypoint.py

# Non-root for sandboxed execution (v2 §10).
RUN useradd --uid 65532 --create-home agent
USER 65532:65532

ENTRYPOINT ["python", "/app/entrypoint.py"]
