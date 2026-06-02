# The agent-runtime image: runs ONE agent via the shared karo-runtime library
# (PRD-KARO-v2.md §4.1, §12). The SDK adapter is the cluster-capable harness.
FROM python:3.12-slim

WORKDIR /app

# Install the shared runtime (same code the CLI uses) with the cluster backend
# extras (Redis/Postgres) + the Claude Agent SDK. The base package imports
# without these; they enable the operator-lane store Protocols (v2 §4.1/§4.2).
COPY karo-runtime/ /app/karo-runtime/
RUN pip install --no-cache-dir "/app/karo-runtime/[sdk,redis,postgres]"

COPY agent-runtime-image/entrypoint.py /app/entrypoint.py

# Non-root for sandboxed execution (v2 §10).
RUN useradd --uid 65532 --create-home agent
USER 65532:65532

ENTRYPOINT ["python", "/app/entrypoint.py"]
