"""File-backed stores for the local CLI runtime (CLI §6.1, §10, §11).

State lives under ``.karo/`` as small JSON/JSONL files so every transition is a
durable, resumable step (CLI §21). Atomic task claiming uses an ``flock``-guarded
critical section plus atomic rename, matching the Postgres ``FOR UPDATE SKIP
LOCKED`` semantics the operator uses (v2 §6) — both pass the shared contract
test.
"""

from __future__ import annotations

import contextlib
import fcntl
import json
import os
import time
from pathlib import Path
from typing import Any, Iterator, Optional

from .base import (
    MemoryRecord,
    Message,
    Task,
    TaskState,
    TERMINAL_STATES,
)


@contextlib.contextmanager
def _flock(lock_path: Path) -> Iterator[None]:
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(lock_path), os.O_CREAT | os.O_RDWR, 0o644)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)


def _atomic_write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + f".tmp.{os.getpid()}")
    tmp.write_text(text, encoding="utf-8")
    os.replace(tmp, path)


# --------------------------------------------------------------------------- #
# Tasks
# --------------------------------------------------------------------------- #
class FileTaskStore:
    def __init__(self, path: str | Path):
        self.root = Path(path)
        self.root.mkdir(parents=True, exist_ok=True)
        self._lock = self.root / ".lock"

    def _task_path(self, task_id: str) -> Path:
        return self.root / f"{task_id}.json"

    def _read_all(self) -> list[Task]:
        tasks = []
        for p in sorted(self.root.glob("task-*.json")):
            tasks.append(Task.from_dict(json.loads(p.read_text("utf-8"))))
        return tasks

    async def create(self, task: Task) -> Task:
        _atomic_write(self._task_path(task.id), json.dumps(task.to_dict()))
        return task

    async def get(self, task_id: str) -> Optional[Task]:
        p = self._task_path(task_id)
        if not p.exists():
            return None
        return Task.from_dict(json.loads(p.read_text("utf-8")))

    async def list(self, state: Optional[str] = None) -> list[Task]:
        tasks = self._read_all()
        if state:
            tasks = [t for t in tasks if t.state == state]
        return sorted(tasks, key=lambda t: t.created)

    async def update(self, task: Task) -> Task:
        task.last_transition = time.time()
        _atomic_write(self._task_path(task.id), json.dumps(task.to_dict()))
        return task

    async def claim(self, owner: str, lease_ttl: float = 60.0) -> Optional[Task]:
        with _flock(self._lock):
            tasks = sorted(self._read_all(), key=lambda t: t.created)
            done_ids = {t.id for t in tasks if t.state == TaskState.done.value}
            now = time.time()
            for t in tasks:
                claimable = t.state == TaskState.pending.value
                # Reclaim a lease whose owner died.
                stale = (
                    t.state == TaskState.assigned.value
                    and t.lease is not None
                    and t.lease < now
                )
                if not (claimable or stale):
                    continue
                if not all(dep in done_ids for dep in t.depends_on):
                    continue
                t.state = TaskState.assigned.value
                t.owner = owner
                t.lease = now + lease_ttl
                t.last_transition = now
                _atomic_write(self._task_path(t.id), json.dumps(t.to_dict()))
                return t
        return None


# --------------------------------------------------------------------------- #
# Mailbox
# --------------------------------------------------------------------------- #
class FileMailboxStore:
    def __init__(self, path: str | Path, hard_limit: int = 500):
        self.root = Path(path)
        self.root.mkdir(parents=True, exist_ok=True)
        self.hard_limit = hard_limit
        self._lock = self.root / ".lock"

    def _box(self, agent: str) -> Path:
        return self.root / f"{agent}.jsonl"

    async def send(self, message: Message) -> Message:
        with _flock(self._lock):
            box = self._box(message.to)
            lines = box.read_text("utf-8").splitlines() if box.exists() else []
            lines.append(json.dumps(message.to_dict()))
            # Aggressive GC beyond hardLimit (trim oldest).
            if len(lines) > self.hard_limit:
                lines = lines[-self.hard_limit :]
            _atomic_write(box, "\n".join(lines) + "\n")
        return message

    async def inbox(self, agent: str, unread_only: bool = False) -> list[Message]:
        box = self._box(agent)
        if not box.exists():
            return []
        msgs = [Message.from_dict(json.loads(l)) for l in box.read_text("utf-8").splitlines() if l]
        return [m for m in msgs if not m.read] if unread_only else msgs

    async def mark_read(self, agent: str, msg_id: str) -> None:
        with _flock(self._lock):
            box = self._box(agent)
            if not box.exists():
                return
            msgs = [Message.from_dict(json.loads(l)) for l in box.read_text("utf-8").splitlines() if l]
            for m in msgs:
                if m.id == msg_id:
                    m.read = True
            _atomic_write(box, "\n".join(json.dumps(m.to_dict()) for m in msgs) + "\n")

    async def purge(self, agent: str) -> None:
        box = self._box(agent)
        if box.exists():
            box.unlink()


# --------------------------------------------------------------------------- #
# Memory
# --------------------------------------------------------------------------- #
class FileMemoryStore:
    def __init__(self, path: str | Path):
        self.root = Path(path)
        self.root.mkdir(parents=True, exist_ok=True)
        self._lock = self.root / ".lock"

    def _log(self, scope: str) -> Path:
        return self.root / f"{scope}.jsonl"

    async def put(self, scope: str, key: str, value: Any, tags=None) -> None:
        rec = MemoryRecord(scope=scope, key=key, value=value, tags=list(tags or []))
        with _flock(self._lock):
            log = self._log(scope)
            with log.open("a", encoding="utf-8") as fh:
                fh.write(json.dumps(rec.to_dict()) + "\n")

    def _records(self, scope: str) -> list[MemoryRecord]:
        log = self._log(scope)
        if not log.exists():
            return []
        return [MemoryRecord.from_dict(json.loads(l)) for l in log.read_text("utf-8").splitlines() if l]

    async def get(self, scope: str, key: str) -> Any:
        latest = None
        for rec in self._records(scope):
            if rec.key == key:
                latest = rec
        return latest.value if latest else None

    async def query(self, scope: str, tags=None, limit=None) -> list[MemoryRecord]:
        recs = self._records(scope)
        if tags:
            wanted = set(tags)
            recs = [r for r in recs if wanted & set(r.tags)]
        recs.sort(key=lambda r: r.ts, reverse=True)
        return recs[:limit] if limit else recs

    async def gc(self, policy: dict[str, Any]) -> None:
        strategy = policy.get("gcStrategy", "none")
        max_items = policy.get("maxItems")
        if strategy == "none" or not max_items:
            return
        with _flock(self._lock):
            for log in self.root.glob("*.jsonl"):
                recs = [MemoryRecord.from_dict(json.loads(l)) for l in log.read_text("utf-8").splitlines() if l]
                if len(recs) <= max_items:
                    continue
                # aggressive / lru: keep the most-recent maxItems.
                recs.sort(key=lambda r: r.ts)
                recs = recs[-max_items:]
                _atomic_write(log, "\n".join(json.dumps(r.to_dict()) for r in recs) + "\n")
