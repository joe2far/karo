#!/bin/bash
set -euo pipefail

echo "[karo-harness] Starting Claw Code agent harness..."

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
# Claw Code reads Anthropic API keys natively via ANTHROPIC_API_KEY.
# For Vertex AI and Bedrock, the AgentInstance controller injects:
#   Vertex AI: CLOUD_ML_REGION, ANTHROPIC_VERTEX_PROJECT_ID
#   Bedrock:   AWS_REGION, AWS_BEDROCK_MODEL_ID
# Claw Code's api-client crate handles OAuth and streaming.

# Map KARO model provider env vars to Claw Code equivalents
if [ -n "${KARO_MODEL_NAME:-}" ]; then
  export CLAW_MODEL="${KARO_MODEL_NAME}"
fi

# --- 2. Generate .claw.json with agent-runtime-mcp sidecar ---
echo "[karo-harness] Generating .claw.json from template..."
cat /etc/karo/mcp-template.json | envsubst > /workspace/.claw.json

# --- 3. Mount CLAW.md from agentConfigFiles (if present) ---
# The AgentInstance controller mounts agentConfigFiles to /workspace/
# Claw Code reads /workspace/CLAW.md automatically (their CLAUDE.md equivalent).
# Also support CLAUDE.md -> CLAW.md symlink for compatibility.
if [ -f /workspace/CLAUDE.md ] && [ ! -f /workspace/CLAW.md ]; then
  ln -s /workspace/CLAUDE.md /workspace/CLAW.md
  echo "[karo-harness] Symlinked CLAUDE.md -> CLAW.md for compatibility."
fi
if [ -f /workspace/CLAW.md ]; then
  echo "[karo-harness] Found CLAW.md in workspace."
fi

# --- 4. Agent loop ---
echo "[karo-harness] Entering agent loop (poll interval: ${KARO_POLL_INTERVAL:-10}s)..."

# Trap SIGTERM for graceful shutdown
SHUTDOWN=0
trap 'echo "[karo-harness] Received SIGTERM, shutting down..."; SHUTDOWN=1' SIGTERM

while [ "$SHUTDOWN" -eq 0 ]; do
  # Poll mailbox for pending messages
  MESSAGES=$(claw -p "Call karo_poll_mailbox with limit 1. If no messages, respond ONLY with the word EMPTY." \
    --allowedTools "mcp__agent-runtime-mcp__karo_poll_mailbox" \
    --max-turns 2 \
    --output-format text 2>/dev/null || echo "EMPTY")

  if echo "$MESSAGES" | grep -q "EMPTY"; then
    sleep "${KARO_POLL_INTERVAL:-10}" &
    wait $! || true
    continue
  fi

  # Build task prompt from message
  TASK_PROMPT=$(karo-build-task-prompt "$MESSAGES")

  echo "[karo-harness] Running task..."

  # Run Claw Code in headless prompt mode
  # Claw Code mirrors Claude Code's CLI interface as a Rust reimplementation.
  # --max-turns limits agent iterations to prevent runaway execution.
  # Default permission mode is DangerFullAccess (suitable for sandboxed pods).
  claw -p "$TASK_PROMPT" \
    --allowedTools "mcp__agent-runtime-mcp__*,Read,Edit,Write,Bash,Search,Glob,Grep" \
    --max-turns "${KARO_MAX_TURNS:-50}" \
    --append-system-prompt "$(cat /workspace/SOUL.md 2>/dev/null || true)" \
    --output-format text || {
      echo "[karo-harness] Task execution failed, reporting failure..."
      claw -p "Call karo_fail_task with reason 'Agent execution failed with non-zero exit code'." \
        --allowedTools "mcp__agent-runtime-mcp__karo_fail_task" \
        --max-turns 2 2>/dev/null || true
    }

  # Report status back to idle
  claw -p "Call karo_report_status with status 'idle'." \
    --allowedTools "mcp__agent-runtime-mcp__karo_report_status" \
    --max-turns 2 2>/dev/null || true

  echo "[karo-harness] Task complete, returning to poll loop."
done

echo "[karo-harness] Shutdown complete."
