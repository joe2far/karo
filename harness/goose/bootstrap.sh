#!/bin/bash
set -euo pipefail

echo "[karo-harness] Starting Goose agent harness..."

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

# --- 1. Generate Goose config from KARO env vars ---
echo "[karo-harness] Generating Goose config..."
mkdir -p ~/.config/goose
karo-generate-goose-config > ~/.config/goose/config.yaml

# --- 2. Register agent-runtime-mcp as Goose extension ---
echo "[karo-harness] Registering agent-runtime-mcp extension..."
goose configure --add-extension agent-runtime-mcp \
  --type stdio --command "/usr/local/bin/agent-runtime-mcp"

# --- 3. Register user MCP tools from ToolSet (injected by AgentInstance controller) ---
if [ -d /etc/karo/tools ]; then
  for tool_config in /etc/karo/tools/*.json; do
    [ -f "$tool_config" ] || continue
    EXT_NAME="$(basename "$tool_config" .json)"
    echo "[karo-harness] Registering user extension: ${EXT_NAME}"
    goose configure --add-extension "${EXT_NAME}" \
      --from-config "$tool_config"
  done
fi

# --- 4. Agent loop ---
echo "[karo-harness] Entering agent loop (poll interval: ${KARO_POLL_INTERVAL:-10}s)..."

# Trap SIGTERM for graceful shutdown
SHUTDOWN=0
trap 'echo "[karo-harness] Received SIGTERM, shutting down..."; SHUTDOWN=1' SIGTERM

while [ "$SHUTDOWN" -eq 0 ]; do
  # Poll mailbox for pending messages
  MESSAGE=$(goose run --no-session -t \
    "Call karo_poll_mailbox with limit 1. If no messages, respond ONLY with the word EMPTY." \
    2>/dev/null || echo "EMPTY")

  if echo "$MESSAGE" | grep -q "EMPTY"; then
    sleep "${KARO_POLL_INTERVAL:-10}" &
    wait $! || true
    continue
  fi

  # Extract task details from the message and build the prompt
  TASK_PROMPT=$(karo-build-task-prompt "$MESSAGE")

  # Determine task type for recipe selection
  TASK_TYPE="${KARO_TASK_TYPE:-default}"
  RECIPE="/etc/karo/recipes/${TASK_TYPE}.yaml"
  if [ ! -f "$RECIPE" ]; then
    RECIPE="/etc/karo/recipes/default.yaml"
  fi

  echo "[karo-harness] Running task (type=${TASK_TYPE}, recipe=${RECIPE})..."

  # Run Goose in headless mode with the task
  GOOSE_MODE=auto GOOSE_MAX_TURNS="${KARO_MAX_TURNS:-50}" \
    goose run --recipe "$RECIPE" --no-session \
    -t "$TASK_PROMPT" || {
      echo "[karo-harness] Task execution failed, reporting failure..."
      goose run --no-session -t \
        "Call karo_fail_task with reason 'Agent execution failed with non-zero exit code'." \
        2>/dev/null || true
    }

  # Report status back to idle
  goose run --no-session -t \
    "Call karo_report_status with status 'idle' and contextTokensUsed from your current usage." \
    2>/dev/null || true

  echo "[karo-harness] Task complete, returning to poll loop."
done

echo "[karo-harness] Shutdown complete."
