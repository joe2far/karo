#!/bin/bash
set -euo pipefail

# Builds a task prompt from a mailbox message returned by karo_poll_mailbox.
# Usage: karo-build-task-prompt "<message_json_or_text>"
#
# The message from karo_poll_mailbox contains task details including:
#   - taskID: the task identifier
#   - taskTitle: human-readable title
#   - taskDescription: detailed description
#   - acceptanceCriteria: criteria for completion
#   - priorFailureNotes: notes from previous failed attempts (if any)
#   - messageID: for acknowledgement

MESSAGE="${1:-}"

if [ -z "$MESSAGE" ]; then
  echo "ERROR: No message provided" >&2
  exit 1
fi

# Read the system prompt from the mounted AgentSpec config (if available)
SYSTEM_PROMPT=""
if [ -f /workspace/CLAUDE.md ]; then
  SYSTEM_PROMPT=$(cat /workspace/CLAUDE.md)
elif [ -f /etc/karo/system-prompt.txt ]; then
  SYSTEM_PROMPT=$(cat /etc/karo/system-prompt.txt)
fi

# Build the complete task prompt
cat <<PROMPT
You are a KARO agent running as Claude Code. You have been assigned a task from your mailbox.

${SYSTEM_PROMPT:+SYSTEM PROMPT:
${SYSTEM_PROMPT}

}MAILBOX MESSAGE:
${MESSAGE}

INSTRUCTIONS:
1. First, acknowledge the message by calling karo_ack_message with the messageID from the mailbox message above.
2. Query memory for relevant context using karo_query_memory if helpful.
3. Execute the task described in the message according to any acceptance criteria.
4. Store key decisions and outcomes using karo_store_memory for future reference.
5. When complete, call karo_complete_task with the taskID and a resultArtifactRef describing what was produced.
6. If you cannot complete the task, call karo_fail_task with the taskID and a clear reason.

Do NOT respond with EMPTY. Execute the task fully.
PROMPT
