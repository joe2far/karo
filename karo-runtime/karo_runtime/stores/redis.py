"""Redis-backed MemoryStore + MailboxStore (operator lane; v2 §6).

Same Protocols as the file backend (`stores/base.py`), so these pass the SAME
conformance + ordering + hardLimit tests via ``tests/store_contract.py`` — the
parity guarantee that "file locally, redis on cluster" behaves identically.

``redis`` is imported lazily so ``import karo_runtime`` works without it; install
with ``pip install karo-runtime[redis]``. Mailbox uses one Redis list per agent
(append-order = delivery order, at-least-once); memory uses one list per scope
(append-only log, latest-wins on ``get``), mirroring the file impl.
"""

from __future__ import annotations

import json
from typing import Any

from .base import MemoryRecord, Message


def _client(url: str):
    try:
        import redis.asyncio as aioredis  # type: ignore
    except Exception as exc:  # pragma: no cover - optional dep
        raise RuntimeError(
            "redis backend requires the 'redis' package: pip install karo-runtime[redis]"
        ) from exc
    return aioredis.from_url(url, decode_responses=True)


class RedisMailboxStore:
    """Per-agent durable mailbox over Redis lists (key ``{ns}:mail:{agent}``)."""

    def __init__(self, url: str, *, hard_limit: int = 500, namespace: str = "karo"):
        self._r = _client(url)
        self.hard_limit = hard_limit
        self.ns = namespace

    def _key(self, agent: str) -> str:
        return f"{self.ns}:mail:{agent}"

    async def send(self, message: Message) -> Message:
        key = self._key(message.to)
        await self._r.rpush(key, json.dumps(message.to_dict()))
        if self.hard_limit:
            # Trim oldest beyond hard_limit (aggressive GC; v2 §6).
            await self._r.ltrim(key, -self.hard_limit, -1)
        return message

    async def inbox(self, agent: str, unread_only: bool = False) -> list[Message]:
        raw = await self._r.lrange(self._key(agent), 0, -1)
        msgs = [Message.from_dict(json.loads(x)) for x in raw]
        return [m for m in msgs if not m.read] if unread_only else msgs

    async def mark_read(self, agent: str, msg_id: str) -> None:
        key = self._key(agent)
        raw = await self._r.lrange(key, 0, -1)
        for i, x in enumerate(raw):
            m = Message.from_dict(json.loads(x))
            if m.id == msg_id:
                m.read = True
                await self._r.lset(key, i, json.dumps(m.to_dict()))
                return

    async def purge(self, agent: str) -> None:
        await self._r.delete(self._key(agent))


class RedisMemoryStore:
    """Team/agent memory over a Redis append-only list (``{ns}:mem:{scope}``)."""

    def __init__(self, url: str, *, namespace: str = "karo"):
        self._r = _client(url)
        self.ns = namespace

    def _key(self, scope: str) -> str:
        return f"{self.ns}:mem:{scope}"

    async def put(self, scope: str, key: str, value: Any, tags=None) -> None:
        rec = MemoryRecord(scope=scope, key=key, value=value, tags=list(tags or []))
        await self._r.rpush(self._key(scope), json.dumps(rec.to_dict()))

    async def _records(self, scope: str) -> list[MemoryRecord]:
        raw = await self._r.lrange(self._key(scope), 0, -1)
        return [MemoryRecord.from_dict(json.loads(x)) for x in raw]

    async def get(self, scope: str, key: str) -> Any:
        latest = None
        for rec in await self._records(scope):
            if rec.key == key:
                latest = rec.value  # latest wins
        return latest

    async def query(self, scope: str, tags=None, limit=None) -> list[MemoryRecord]:
        recs = await self._records(scope)
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
        # Aggressive/LRU: keep the most-recent max_items per scope, across all
        # scopes this store owns (mirrors the file backend's per-log GC).
        async for key in self._r.scan_iter(match=f"{self.ns}:mem:*"):
            await self._r.ltrim(key, -int(max_items), -1)
