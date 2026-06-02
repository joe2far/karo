"""Postgres-backed stores — the default cluster backend for tasks, memory AND
mailbox (operator lane; v2 §6).

One Postgres instance backs all three coordination stores, so a cluster needs a
single stateful dependency (Redis is available in ``stores/redis.py`` but is not
the default — it can be wired in later for mailbox/memory if desired). Each store
implements the SAME karo_runtime Protocol as the file backend and passes the SAME
conformance tests — including the atomic-claim concurrency test, where the claim
uses the canonical ``SELECT ... FOR UPDATE SKIP LOCKED`` so N parallel pods never
run one task twice (v2 §6/§17).

``asyncpg`` is imported lazily; install with ``pip install karo-runtime[postgres]``.
State is the authoritative source of truth (every transition is a committed row);
``AgentTask`` CRDs are a projection the controller updates for visibility.
"""

from __future__ import annotations

import json
import time
from typing import Any, Optional

from .base import MemoryRecord, Message, Task, TaskState


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


class PostgresMemoryStore:
    """Team/agent memory over Postgres (append-only log, latest-wins on ``get``).

    Same Protocol as the file/redis memory stores (CLI §10, v2 §6).
    """

    def __init__(self, dsn: str, *, table: str = "memory"):
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
                        seq    BIGSERIAL PRIMARY KEY,
                        id     TEXT NOT NULL,
                        scope  TEXT NOT NULL,
                        key    TEXT NOT NULL,
                        value  JSONB,
                        tags   JSONB NOT NULL DEFAULT '[]',
                        ts     DOUBLE PRECISION NOT NULL
                    )
                    """
                )
        return self._pool

    async def put(self, scope: str, key: str, value: Any, tags=None) -> None:
        rec = MemoryRecord(scope=scope, key=key, value=value, tags=list(tags or []))
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(
                f"INSERT INTO {self.table} (id,scope,key,value,tags,ts) VALUES ($1,$2,$3,$4,$5,$6)",
                rec.id, rec.scope, rec.key, json.dumps(rec.value), json.dumps(rec.tags), rec.ts,
            )

    async def get(self, scope: str, key: str) -> Any:
        pool = await self._ensure()
        async with pool.acquire() as con:
            row = await con.fetchrow(
                f"SELECT value FROM {self.table} WHERE scope=$1 AND key=$2 ORDER BY seq DESC LIMIT 1",
                scope, key,
            )
        return json.loads(row["value"]) if row and row["value"] is not None else None

    async def query(self, scope: str, tags=None, limit=None) -> list[MemoryRecord]:
        pool = await self._ensure()
        async with pool.acquire() as con:
            rows = await con.fetch(
                f"SELECT id,scope,key,value,tags,ts FROM {self.table} WHERE scope=$1 ORDER BY seq",
                scope,
            )
        recs = [
            MemoryRecord(
                id=r["id"], scope=r["scope"], key=r["key"],
                value=json.loads(r["value"]) if r["value"] is not None else None,
                tags=json.loads(r["tags"]), ts=r["ts"],
            )
            for r in rows
        ]
        if tags:
            want = set(tags)
            recs = [r for r in recs if want & set(r.tags)]
        if limit is not None:
            recs = recs[-limit:]
        return recs

    async def gc(self, policy: dict[str, Any]) -> None:
        max_items = policy.get("maxItems")
        if policy.get("gcStrategy") in (None, "none") or not max_items:
            return
        # Keep the most-recent max_items per scope (aggressive/lru; v2 §6).
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(
                f"""
                DELETE FROM {self.table} t
                WHERE t.seq NOT IN (
                    SELECT seq FROM (
                        SELECT seq, row_number() OVER (PARTITION BY scope ORDER BY seq DESC) AS rn
                        FROM {self.table}
                    ) ranked WHERE rn <= $1
                )
                """,
                int(max_items),
            )


class PostgresMailboxStore:
    """Per-agent durable mailbox over Postgres (append-order = delivery order).

    Same Protocol as the file/redis mailbox stores; at-least-once, ``hardLimit``
    trims oldest (aggressive GC; CLI §11, v2 §6).
    """

    def __init__(self, dsn: str, *, hard_limit: int = 500, table: str = "mailbox"):
        self.dsn = dsn
        self.hard_limit = hard_limit
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
                        seq     BIGSERIAL PRIMARY KEY,
                        id      TEXT NOT NULL,
                        agent   TEXT NOT NULL,
                        body    TEXT NOT NULL,
                        sender  TEXT NOT NULL,
                        ts      DOUBLE PRECISION NOT NULL,
                        read    BOOLEAN NOT NULL DEFAULT FALSE
                    )
                    """
                )
        return self._pool

    async def send(self, message: Message) -> Message:
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(
                f"INSERT INTO {self.table} (id,agent,body,sender,ts,read) VALUES ($1,$2,$3,$4,$5,$6)",
                message.id, message.to, message.body, message.sender, message.ts, message.read,
            )
            if self.hard_limit:
                await con.execute(
                    f"""
                    DELETE FROM {self.table} t
                    WHERE t.agent=$1 AND t.seq NOT IN (
                        SELECT seq FROM {self.table} WHERE agent=$1 ORDER BY seq DESC LIMIT $2
                    )
                    """,
                    message.to, int(self.hard_limit),
                )
        return message

    async def inbox(self, agent: str, unread_only: bool = False) -> list[Message]:
        pool = await self._ensure()
        q = f"SELECT id,agent,body,sender,ts,read FROM {self.table} WHERE agent=$1"
        if unread_only:
            q += " AND read=FALSE"
        q += " ORDER BY seq"
        async with pool.acquire() as con:
            rows = await con.fetch(q, agent)
        return [
            Message(id=r["id"], to=r["agent"], body=r["body"], sender=r["sender"], ts=r["ts"], read=r["read"])
            for r in rows
        ]

    async def mark_read(self, agent: str, msg_id: str) -> None:
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(
                f"UPDATE {self.table} SET read=TRUE WHERE agent=$1 AND id=$2", agent, msg_id
            )

    async def purge(self, agent: str) -> None:
        pool = await self._ensure()
        async with pool.acquire() as con:
            await con.execute(f"DELETE FROM {self.table} WHERE agent=$1", agent)
