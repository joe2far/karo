# The agent-runtime image: runs ONE agent via the shared karo-runtime library
# (PRD-KARO-v2.md §4.1, §12). The SDK adapter is the cluster-capable harness.
FROM python:3.12-slim

WORKDIR /app

# Install the shared runtime (same code the CLI uses) + the Claude Agent SDK.
COPY karo-runtime/ /app/karo-runtime/
RUN pip install --no-cache-dir /app/karo-runtime/ \
    && pip install --no-cache-dir "claude-agent-sdk>=0.1.0" || true

COPY agent-runtime-image/entrypoint.py /app/entrypoint.py

# Non-root for sandboxed execution (v2 §10).
RUN useradd --uid 65532 --create-home agent
USER 65532:65532

ENTRYPOINT ["python", "/app/entrypoint.py"]
