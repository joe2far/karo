"""Postgres-backed TaskStore (operator lane; v2 §6).

Same Protocol as the file backend, so it passes the SAME conformance tests
including the atomic-claim concurrency test — here the claim uses the canonical
``SELECT ... FOR UPDATE SKIP LOCKED`` so N parallel pods never run one task twice
(v2 §6/§17). ``asyncpg`` is imported lazily; install with
``pip install karo-runtime[postgres]``.

State is the authoritative source of truth (every transition is a committed row);
``AgentTask`` CRDs are a projection the controller updates for visibility.
"""

from __future__ import annotations

import json
import time
from typing import Optional

from .base import Task, TaskState


def _connect_fn():
    try:
        import asyncpg  # type: ignore
    except Exception as exc:  # pragma: no cover - optional dep
        raise RuntimeError(
            "postgres backend requires 'asyncpg': pip install karo-runtime[postgres]"
        ) from exc
    return asyncpg


class PostgresTaskStore:
    """Durable task store. One table per ``namespace`` (default ``tasks``)."""

    def __init__(self, dsn: str, *, table: str = "tasks"):
        self.dsn = dsn
        self.table = table
        self._asyncpg = _connect_fn()
        self._pool = None

    async def _ensure(self):
        if self._pool is None:
            self._pool = await self._asyncpg.create_pool(self.dsn)
            async with self._pool.acquire() as con:
                await con.execute(
                    f"""
                    CREATE TABLE IF NOT EXISTS {self.table} (
                        id            TEXT PRIMARY KEY,
                        state         TEXT NOT NULL,
                        owner         TEXT,
                        lease         DOUBLE PRECISION,
                        created       DOUBLE PRECISION NOT NULL,
                        depends_on    JSONB NOT NULL DEFAULT '[]',
                        doc           JSONB NOT NULL
                    )
                    """
                )
        return self._pool

    async def create(self, task: Task) -> Task:
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(
                f"INSERT INTO {self.table} (id,state,owner,lease,created,depends_on,doc) "
                "VALUES ($1,$2,$3,$4,$5,$6,$7)",
                task.id, task.state, task.owner, task.lease, task.created,
                json.dumps(task.depends_on), json.dumps(task.to_dict()),
            )
        return task

    async def get(self, task_id: str) -> Optional[Task]:
        pool = await self._ensure()
        async with pool.acquire() as con:
            row = await con.fetchrow(f"SELECT doc FROM {self.table} WHERE id=$1", task_id)
        return Task.from_dict(json.loads(row["doc"])) if row else None

    async def list(self, state: Optional[str] = None) -> list[Task]:
        pool = await self._ensure()
        async with pool.acquire() as con:
            if state is None:
                rows = await con.fetch(f"SELECT doc FROM {self.table} ORDER BY created")
            else:
                rows = await con.fetch(
                    f"SELECT doc FROM {self.table} WHERE state=$1 ORDER BY created", state
                )
        return [Task.from_dict(json.loads(r["doc"])) for r in rows]

    async def update(self, task: Task) -> Task:
        task.last_transition = time.time()
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(
                f"UPDATE {self.table} SET state=$2,owner=$3,lease=$4,depends_on=$5,doc=$6 WHERE id=$1",
                task.id, task.state, task.owner, task.lease,
                json.dumps(task.depends_on), json.dumps(task.to_dict()),
            )
        return task

    async def claim(self, owner: str, lease_ttl: float = 60.0) -> Optional[Task]:
        """Atomically claim the next ready pending task (FOR UPDATE SKIP LOCKED).

        A task is ready when state='pending' and every dependency is 'done'. A
        stale lease (expired) on an assigned task is reclaimable. Two claimants
        never get the same row.
        """
        now = time.time()
        lease = now + lease_ttl
        pool = await self._ensure()
        async with pool.acquire() as con:
            async with con.transaction():
                row = await con.fetchrow(
                    f"""
                    SELECT t.id, t.doc FROM {self.table} t
                    WHERE (
                        t.state = 'pending'
                        OR (t.state = 'assigned' AND t.lease IS NOT NULL AND t.lease < $1)
                    )
                    AND NOT EXISTS (
                        SELECT 1 FROM jsonb_array_elements_text(t.depends_on) dep
                        WHERE (SELECT state FROM {self.table} d WHERE d.id = dep) IS DISTINCT FROM 'done'
                    )
                    ORDER BY t.created
                    FOR UPDATE SKIP LOCKED
                    LIMIT 1
                    """,
                    now,
                )
                if row is None:
                    return None
                task = Task.from_dict(json.loads(row["doc"]))
                task.state = TaskState.assigned.value
                task.owner = owner
                task.lease = lease
                task.last_transition = now
                await con.execute(
                    f"UPDATE {self.table} SET state=$2,owner=$3,lease=$4,doc=$5 WHERE id=$1",
                    task.id, task.state, task.owner, task.lease, json.dumps(task.to_dict()),
                )
                return task
