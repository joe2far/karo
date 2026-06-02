"""The ``sdk`` harness adapter — wraps the Claude Agent SDK (CLI §6.2).

The SDK adapter is the only guaranteed cluster-capable harness (§4.7); the
operator's agent-runtime image *is* this adapter. When the Claude Agent SDK or
credentials are unavailable (e.g. ``--dry-run``, CI without keys), the adapter
degrades to a deterministic local stub so the Coordinator, task graph, and store
transitions remain fully exercisable offline.
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


def _sdk_available() -> bool:
    try:  # pragma: no cover - depends on optional dependency
        import claude_agent_sdk  # noqa: F401

        return True
    except Exception:
        return False


def _estimate_tokens(text: str) -> int:
    # ~4 chars/token heuristic for harnesses/paths that don't report usage (§8).
    return max(1, len(text) // 4)


class SdkAdapter:
    name = "sdk"

    def __init__(self, *, dry_run: bool = False):
        self.dry_run = dry_run
        self._use_sdk = (not dry_run) and _sdk_available()

    def capabilities(self) -> HarnessCapabilities:
        return HarnessCapabilities(
            tools=True,
            mcp=True,
            skills=True,
            streaming=True,
            attach=True,
            cluster_capable=True,
        )

    def supports_model(self, model: dict[str, Any]) -> bool:
        # The SDK accepts arbitrary provider/model bindings via the router.
        return True

    async def run_turn(self, ctx: AgentContext, message: Message) -> TurnResult:
        if self._use_sdk:  # pragma: no cover - requires creds + SDK
            return await self._run_sdk_turn(ctx, message)
        return self._run_stub_turn(ctx, message)

    def _run_stub_turn(self, ctx: AgentContext, message: Message) -> TurnResult:
        text = (
            f"[stub:{ctx.agent_name}] processed objective. "
            f"(model={ctx.model.get('id', '?')}) -> {message.content[:120]}"
        )
        return TurnResult(
            text=text,
            prompt_tokens=_estimate_tokens(ctx.instructions + message.content),
            completion_tokens=_estimate_tokens(text),
            estimated=True,
        )

    async def _run_sdk_turn(self, ctx: AgentContext, message: Message) -> TurnResult:  # pragma: no cover
        from claude_agent_sdk import ClaudeAgentOptions, query

        chunks: list[str] = []
        prompt = f"{message.content}"
        options = ClaudeAgentOptions(
            system_prompt=ctx.instructions,
            permission_mode=ctx.permission_mode,
            cwd=ctx.working_dir,
        )
        prompt_tokens = completion_tokens = 0
        async for msg in query(prompt=prompt, options=options):
            text = getattr(msg, "result", None) or getattr(msg, "text", None)
            if text:
                chunks.append(text)
            usage = getattr(msg, "usage", None)
            if usage:
                prompt_tokens += getattr(usage, "input_tokens", 0)
                completion_tokens += getattr(usage, "output_tokens", 0)
        out = "".join(chunks)
        return TurnResult(
            text=out,
            prompt_tokens=prompt_tokens or _estimate_tokens(prompt),
            completion_tokens=completion_tokens or _estimate_tokens(out),
            estimated=not (prompt_tokens or completion_tokens),
        )

    async def stream(self, ctx: AgentContext, message: Message) -> AsyncIterator[Event]:
        result = await self.run_turn(ctx, message)
        yield Event(type="turn.delta", data={"text": result.text})
        yield Event(type="turn.end", data={"tokens": result.completion_tokens})

    async def interrupt(self) -> None:
        return None

    async def attach(self, ctx: AgentContext) -> AttachSession:
        return AttachSession(ctx)
