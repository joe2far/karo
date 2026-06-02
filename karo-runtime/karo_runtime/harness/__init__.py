"""Harness adapters behind the shared ``HarnessAdapter`` Protocol."""

from .base import (
    AgentContext,
    AttachSession,
    HarnessAdapter,
    HarnessCapabilities,
    Message,
    TurnResult,
)
from .local_adapters import ClaudeCodeAdapter, CodexAdapter, CursorAdapter
from .sdk_adapter import SdkAdapter


def get_adapter(harness: str, *, dry_run: bool = False) -> HarnessAdapter:
    """Return an adapter instance for the named harness."""
    if harness == "sdk":
        return SdkAdapter(dry_run=dry_run)
    if harness == "claude-code":
        return ClaudeCodeAdapter()
    if harness == "cursor":
        return CursorAdapter()
    if harness == "codex":
        return CodexAdapter()
    raise ValueError(f"unknown harness: {harness!r}")


__all__ = [
    "HarnessAdapter",
    "HarnessCapabilities",
    "AgentContext",
    "AttachSession",
    "Message",
    "TurnResult",
    "SdkAdapter",
    "ClaudeCodeAdapter",
    "CursorAdapter",
    "CodexAdapter",
    "get_adapter",
]
