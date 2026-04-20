#!/bin/bash
set -euo pipefail

echo "[karo-harness] Starting OpenCode agent harness..."

# --- Wait for MCP sidecar to be ready ---
MCP_READYZ_URL="http://localhost:9091/readyz"
MAX_WAIT="${KARO_SIDECAR_TIMEOUT:-120}"
WAITED=0

echo "[karo-harness] Waiting for agent-runtime-mcp sidecar at ${MCP_READYZ_URL}..."
until curl -sf "${MCP_READYZ_URL}" > /dev/null 2>&1; do
  if [ "$WAITED" -ge "$MAX_WAIT" ]; then
    echo "[karo-harness] ERROR: MCP sidecar not ready after ${MAX_WAIT}s. Exiting."
    exit 1
  fi
  sleep 2
  WAITED=$((WAITED + 2))
done
echo "[karo-harness] MCP sidecar is ready (waited ${WAITED}s)."

# --- 1. Configure model provider from KARO ModelConfig env vars ---
# The AgentInstance controller injects provider-specific env vars:
#   Anthropic direct: ANTHROPIC_API_KEY
#   OpenAI:           OPENAI_API_KEY
#   Vertex AI:        GOOGLE_APPLICATION_CREDENTIALS, CLOUD_ML_REGION, ANTHROPIC_VERTEX_PROJECT_ID
#   Bedrock:          AWS_REGION, AWS_BEDROCK_MODEL_ID, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY
# OpenCode reads these natively via its provider auto-detection.

# Map KARO model provider env vars to OpenCode's expected model selector
if [ -n "${KARO_MODEL_NAME:-}" ]; then
  export OPENCODE_MODEL="${KARO_MODEL_NAME}"
fi

# --- 2. Generate opencode.json with agent-runtime-mcp sidecar ---
echo "[karo-harness] Generating opencode.json from template..."
cat /etc/karo/mcp-template.json | envsubst > /workspace/opencode.json

# --- 3. Mount AGENTS.md from agentConfigFiles (if present) ---
# The AgentInstance controller mounts agentConfigFiles to /workspace/
# OpenCode reads /workspace/AGENTS.md automatically (its convention for
# repository instructions, equivalent to CLAUDE.md).
# Also support CLAUDE.md -> AGENTS.md symlink for cross-harness compatibility.
if [ -f /workspace/CLAUDE.md ] && [ ! -f /workspace/AGENTS.md ]; then
  ln -s /workspace/CLAUDE.md /workspace/AGENTS.md
  echo "[karo-harness] Symlinked CLAUDE.md -> AGENTS.md for compatibility."
fi
if [ -f /workspace/AGENTS.md ]; then
  echo "[karo-harness] Found AGENTS.md in workspace."
fi

# --- 4. Agent loop ---
echo "[karo-harness] Entering agent loop (poll interval: ${KARO_POLL_INTERVAL:-10}s)..."

# Trap SIGTERM for graceful shutdown
SHUTDOWN=0
trap 'echo "[karo-harness] Received SIGTERM, shutting down..."; SHUTDOWN=1' SIGTERM

# Optional model flag (OpenCode accepts provider/model via --model)
MODEL_FLAG=()
if [ -n "${OPENCODE_MODEL:-}" ]; then
  MODEL_FLAG=(--model "${OPENCODE_MODEL}")
fi

while [ "$SHUTDOWN" -eq 0 ]; do
  # Poll mailbox for pending messages
  MESSAGES=$(opencode run "${MODEL_FLAG[@]}" \
    "Call karo_poll_mailbox with limit 1. If no messages, respond ONLY with the word EMPTY." \
    2>/dev/null || echo "EMPTY")

  if echo "$MESSAGES" | grep -q "EMPTY"; then
    sleep "${KARO_POLL_INTERVAL:-10}" &
    wait $! || true
    continue
  fi

  # Build task prompt from message
  TASK_PROMPT=$(karo-build-task-prompt "$MESSAGES")

  echo "[karo-harness] Running task..."

  # Run OpenCode in headless mode
  # `opencode run` is the non-interactive subcommand suitable for
  # autonomous execution inside a sandboxed pod.
  opencode run "${MODEL_FLAG[@]}" "$TASK_PROMPT" || {
      echo "[karo-harness] Task execution failed, reporting failure..."
      opencode run "${MODEL_FLAG[@]}" \
        "Call karo_fail_task with reason 'Agent execution failed with non-zero exit code'." \
        2>/dev/null || true
    }

  # Report status back to idle
  opencode run "${MODEL_FLAG[@]}" \
    "Call karo_report_status with status 'idle'." \
    2>/dev/null || true

  echo "[karo-harness] Task complete, returning to poll loop."
done

echo "[karo-harness] Shutdown complete."
