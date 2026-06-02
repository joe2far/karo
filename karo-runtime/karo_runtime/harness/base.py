"""The ``HarnessAdapter`` Protocol — the key extensibility seam (CLI §6.2).

All harness differences (including cluster-capability) hide behind this
Protocol; no harness-specific logic leaks into the Coordinator/Planner (CLI §21).
Adding a new harness = implementing this Protocol.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Protocol, runtime_checkable


@dataclass
class HarnessCapabilities:
    tools: bool = True
    mcp: bool = True
    skills: bool = True
    streaming: bool = True
    attach: bool = True
    cluster_capable: bool = False  # gates `karo export` (§4.7)


@dataclass
class Message:
    role: str  # "user" | "system"
    content: str


@dataclass
class TurnResult:
    text: str
    prompt_tokens: int = 0
    completion_tokens: int = 0
    estimated: bool = False
    tool_calls: list[dict[str, Any]] = field(default_factory=list)


@dataclass
class Event:
    type: str
    data: dict[str, Any] = field(default_factory=dict)


@dataclass
class AgentContext:
    """Everything an adapter needs to run one agent turn (CLI §6.2)."""

    agent_name: str
    instructions: str
    model: dict[str, Any]
    tools: list[str] = field(default_factory=list)
    mcp: list[str] = field(default_factory=list)
    skills: list[str] = field(default_factory=list)
    working_dir: str = "./workspace"
    permission_mode: str = "prompt"
    # Accessors are injected by the Coordinator; kept loose to avoid import cycles.
    memory: Any = None
    mailbox: Any = None
    budget: Any = None
    extras: dict[str, Any] = field(default_factory=dict)


class AttachSession:
    """A live attach handle: stream + inject + interrupt + detach (CLI §13).

    Locally this is an in-process session over the same adapter the Coordinator
    drives; on cluster the operator wraps the identical verbs over a streamed
    transport (websocket/SPDY) to the agent pod (v2 §7). Injecting feeds a user
    turn into the agent loop; the running turn can be interrupted; detaching
    hands control back to the Coordinator.
    """

    def __init__(self, adapter: "HarnessAdapter", ctx: AgentContext):
        self.adapter = adapter
        self.ctx = ctx
        self.detached = False
        self.interrupted = False
        self.injected: list[str] = []
        self.transcript: list[Event] = []

    async def inject(self, body: str) -> TurnResult:
        """Inject a human user-turn into the agent loop and capture the reply."""
        if self.detached:
            raise RuntimeError("cannot inject on a detached session")
        self.interrupted = False
        self.injected.append(body)
        self.transcript.append(Event(type="human.inject", data={"body": body}))
        result = await self.adapter.run_turn(self.ctx, Message("user", body))
        self.transcript.append(Event(type="turn.delta", data={"text": result.text}))
        return result

    async def interrupt(self) -> None:
        """Interrupt the current turn (stop generation)."""
        self.interrupted = True
        await self.adapter.interrupt()

    async def stream(self) -> AsyncIterator[Event]:
        """Replay the live session transcript (watch the stream)."""
        for event in self.transcript:
            yield event

    def detach(self) -> None:
        self.detached = True


@runtime_checkable
class HarnessAdapter(Protocol):
    name: str

    async def run_turn(self, ctx: AgentContext, message: Message) -> TurnResult: ...
    def stream(self, ctx: AgentContext, message: Message) -> AsyncIterator[Event]: ...
    async def interrupt(self) -> None: ...
    async def attach(self, ctx: AgentContext) -> AttachSession: ...
    def supports_model(self, model: dict[str, Any]) -> bool: ...
    def capabilities(self) -> HarnessCapabilities: ...
