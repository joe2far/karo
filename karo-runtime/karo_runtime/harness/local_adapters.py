"""Local-only harness adapters: ``claude-code``, ``cursor``, ``codex``.

These drive interactive desktop/TUI tools and are **local-only** per the
portability matrix (§4.7): they advertise ``cluster_capable=False`` so
``karo validate --target cluster`` / ``karo export`` reject them for the cluster.
v1 ships them as thin shells; the full PTY/pane attach lands in M3.
"""

from __future__ import annotations

from typing import Any, AsyncIterator

from .base import (
    AgentContext,
    AttachSession,
    Event,
    HarnessCapabilities,
    Message,
    TurnResult,
)


class _ShellHarness:
    name = "shell"

    def capabilities(self) -> HarnessCapabilities:
        return HarnessCapabilities(cluster_capable=False)

    def supports_model(self, model: dict[str, Any]) -> bool:
        return True

    async def run_turn(self, ctx: AgentContext, message: Message) -> TurnResult:
        text = f"[{self.name}:{ctx.agent_name}] (local harness) {message.content[:120]}"
        return TurnResult(text=text, estimated=True, completion_tokens=len(text) // 4)

    async def stream(self, ctx: AgentContext, message: Message) -> AsyncIterator[Event]:
        r = await self.run_turn(ctx, message)
        yield Event(type="turn.end", data={"text": r.text})

    async def interrupt(self) -> None:
        return None

    async def attach(self, ctx: AgentContext) -> AttachSession:
        # Local-only: the full impl drops into the tool's native TUI (PTY/pane,
        # M3). The streamed inject/interrupt/detach verbs work in-process here.
        return AttachSession(self, ctx)


class ClaudeCodeAdapter(_ShellHarness):
    name = "claude-code"


class CursorAdapter(_ShellHarness):
    name = "cursor"


class CodexAdapter(_ShellHarness):
    name = "codex"
