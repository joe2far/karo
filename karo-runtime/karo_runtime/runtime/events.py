"""Canonical event vocabulary (CLI §15) — shared local & cluster.

The operator maps these 1:1 to metrics/traces (v2 §9), so dashboards match
across local and cluster. Events are written to
``.karo/runs/<run-id>/events.jsonl``.
"""

from __future__ import annotations

import hashlib
import json
import time
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path
from typing import Any, Optional


class EventType(str, Enum):
    turn_start = "turn.start"
    turn_end = "turn.end"
    tool_call = "tool.call"
    mailbox_send = "mailbox.send"
    mailbox_recv = "mailbox.recv"
    task_transition = "task.transition"
    attach = "attach"
    detach = "detach"
    guard_pause = "guard.pause"
    human_inject = "human.inject"
    model_usage = "model.usage"
    budget_halt = "budget.halt"


def digest(value: Any) -> str:
    """Short, stable digest for args/bodies we don't want to log verbatim."""
    raw = json.dumps(value, sort_keys=True, default=str).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()[:12]


@dataclass
class Event:
    type: str
    run: str
    agent: Optional[str] = None
    ts: float = field(default_factory=time.time)
    fields: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        d = {"ts": self.ts, "type": self.type, "run": self.run, "agent": self.agent}
        d.update(self.fields)
        return d


class EventLog:
    """Append-only JSONL event log for one run, plus an optional live callback."""

    def __init__(self, run_id: str, root: str | Path = ".karo/runs", on_event=None):
        self.run_id = run_id
        self.dir = Path(root) / run_id
        self.dir.mkdir(parents=True, exist_ok=True)
        self.path = self.dir / "events.jsonl"
        self._on_event = on_event

    def emit(self, type_: str, agent: Optional[str] = None, **fields: Any) -> Event:
        ev = Event(type=type_, run=self.run_id, agent=agent, fields=fields)
        with self.path.open("a", encoding="utf-8") as fh:
            fh.write(json.dumps(ev.to_dict()) + "\n")
        if self._on_event:
            self._on_event(ev)
        return ev
