"""Backend-agnostic store conformance harness (CLI §11, v2 §6/§13/§15).

The store *contract* is defined once, in ``test_stores.py``, and run against every
backend in ``ALL_BACKENDS``. The file backend is wired today; M1 appends a
``RedisBackend`` (mailbox/memory) and ``PostgresBackend`` (tasks) here, and the
identical conformance + atomic-claim tests run against them with **zero new
assertions** — which is what makes "same contract, file locally / redis+pg on
cluster" a tested invariant rather than a docstring claim.

To add a backend: implement ``StoreBackend`` and append an instance to
``ALL_BACKENDS`` (set ``available=False`` with a ``skip_reason`` when its server
isn't reachable, so CI skips rather than fails).
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any

from karo_runtime.stores.file import (
    FileMailboxStore,
    FileMemoryStore,
    FileTaskStore,
)


@dataclass
class StoreBackend:
    """Factory for one set of store implementations under test."""

    name: str
    available: bool = True
    skip_reason: str = ""

    def task_store(self, tmp_path: Path) -> Any:  # pragma: no cover - overridden
        raise NotImplementedError

    def mailbox_store(self, tmp_path: Path, hard_limit: int = 500) -> Any:  # pragma: no cover
        raise NotImplementedError

    def memory_store(self, tmp_path: Path) -> Any:  # pragma: no cover
        raise NotImplementedError


class FileBackend(StoreBackend):
    def __init__(self) -> None:
        super().__init__(name="file")

    def task_store(self, tmp_path: Path) -> Any:
        return FileTaskStore(tmp_path / "tasks")

    def mailbox_store(self, tmp_path: Path, hard_limit: int = 500) -> Any:
        return FileMailboxStore(tmp_path / "mail", hard_limit=hard_limit)

    def memory_store(self, tmp_path: Path) -> Any:
        return FileMemoryStore(tmp_path / "mem")


# Backends the conformance suite runs against. M1 appends Redis/Postgres here.
ALL_BACKENDS: list[StoreBackend] = [FileBackend()]
