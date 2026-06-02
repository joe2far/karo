"""Store Protocols + core data types (M0 shared foundation).

The CLI uses the file backend; the operator uses Redis/Postgres backends. Both
implement these identical Protocols and pass the same contract tests — including
the atomic-claim concurrency test — so behavior is parity-identical (CLI §10/§11,
v2 §6).
"""

from __future__ import annotations

import time
import uuid
from dataclasses import asdict, dataclass, field
from enum import Enum
from typing import Any, Optional, Protocol, runtime_checkable


class TaskState(str, Enum):
    pending = "pending"
    assigned = "assigned"
    in_progress = "in-progress"
    blocked = "blocked"
    review = "review"
    paused = "paused"  # awaiting human attach (distinct from review)
    done = "done"
    failed = "failed"
    cancelled = "cancelled"


TERMINAL_STATES = {TaskState.done, TaskState.failed, TaskState.cancelled}


def _now() -> float:
    return time.time()


def _id(prefix: str) -> str:
    return f"{prefix}-{uuid.uuid4().hex[:12]}"


@dataclass
class Task:
    objective: str
    owner: Optional[str] = None
    reviewer: Optional[str] = None  # reviewer agent for the `review` state (M2)
    acceptance_criteria: list[str] = field(default_factory=list)
    depends_on: list[str] = field(default_factory=list)
    state: str = TaskState.pending.value
    attempts: int = 0
    lease: Optional[float] = None
    attached_by: Optional[str] = None
    # Set once a pauseBefore guard has been satisfied (via attach/continue) so the
    # task is not re-paused on resume; persisted so resume across processes works.
    guard_released: bool = False
    # Why the task is paused: "guard:pauseBefore" | "guard:pauseOn" | "budget".
    # Distinguishes a guard pause (released by attach) from a budget pause
    # (re-gated on resume) so they are not conflated.
    pause_reason: Optional[str] = None
    result: Optional[dict[str, Any]] = None
    id: str = field(default_factory=lambda: _id("task"))
    created: float = field(default_factory=_now)
    last_transition: float = field(default_factory=_now)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Task":
        return cls(**d)


@dataclass
class Message:
    to: str
    body: str
    sender: str = "system"
    id: str = field(default_factory=lambda: _id("msg"))
    ts: float = field(default_factory=_now)
    read: bool = False

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Message":
        return cls(**d)


@dataclass
class MemoryRecord:
    scope: str
    key: str
    value: Any
    tags: list[str] = field(default_factory=list)
    id: str = field(default_factory=lambda: _id("mem"))
    ts: float = field(default_factory=_now)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "MemoryRecord":
        return cls(**d)


@runtime_checkable
class MemoryStore(Protocol):
    async def put(self, scope: str, key: str, value: Any, tags=None) -> None: ...
    async def get(self, scope: str, key: str) -> Any: ...
    async def query(self, scope: str, tags=None, limit=None) -> list[MemoryRecord]: ...
    async def gc(self, policy: dict[str, Any]) -> None: ...


@runtime_checkable
class TaskStore(Protocol):
    async def create(self, task: Task) -> Task: ...
    async def get(self, task_id: str) -> Optional[Task]: ...
    async def list(self, state: Optional[str] = None) -> list[Task]: ...
    async def update(self, task: Task) -> Task: ...
    async def claim(
        self, owner: str, lease_ttl: float = 60.0, agent: Optional[str] = None
    ) -> Optional[Task]:
        """Atomically claim the next ready ``pending`` task, or return None.

        When ``agent`` is set, only tasks owned by ``agent`` (or unowned) are
        eligible — so a cluster pod claims only its agent's work while unowned
        (swarm) tasks remain first-available. ``owner`` is the claimant identity
        recorded on the task.
        """
        ...


@runtime_checkable
class MailboxStore(Protocol):
    async def send(self, message: Message) -> Message: ...
    async def inbox(self, agent: str, unread_only: bool = False) -> list[Message]: ...
    async def mark_read(self, agent: str, msg_id: str) -> None: ...
    async def purge(self, agent: str) -> None: ...
