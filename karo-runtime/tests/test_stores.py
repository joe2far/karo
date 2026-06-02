"""Store conformance contract — runs against every backend in ALL_BACKENDS.

This IS the shared contract suite the design relies on (CLI §11, v2 §6/§13): the
file backend runs it today; Redis/Postgres (M1) run the *same* assertions by
appending to ``store_contract.ALL_BACKENDS`` — including the atomic-claim
concurrency test, the core swarm safety guarantee.
"""

import asyncio
from pathlib import Path

import pytest

from karo_runtime.stores.base import Message, Task, TaskState

from store_contract import ALL_BACKENDS


@pytest.fixture(params=ALL_BACKENDS, ids=lambda b: b.name)
def backend(request):
    b = request.param
    if not b.available:
        pytest.skip(b.skip_reason or f"{b.name} backend unavailable")
    return b


async def test_task_crud_and_transition(backend, tmp_path: Path):
    store = backend.task_store(tmp_path)
    t = await store.create(Task(objective="do it"))
    assert (await store.get(t.id)).objective == "do it"
    t.state = TaskState.done.value
    await store.update(t)
    assert (await store.get(t.id)).state == "done"
    assert len(await store.list(state="done")) == 1


async def test_atomic_claim_no_double_execution(backend, tmp_path: Path):
    """No two claimants ever get the same task (the core swarm guarantee)."""
    store = backend.task_store(tmp_path)
    n = 25
    for _ in range(n):
        await store.create(Task(objective="work"))

    async def worker(name: str) -> list[str]:
        claimed = []
        while True:
            t = await store.claim(owner=name)
            if t is None:
                break
            claimed.append(t.id)
        return claimed

    results = await asyncio.gather(*[worker(f"w{i}") for i in range(8)])
    flat = [tid for r in results for tid in r]
    assert len(flat) == n
    assert len(set(flat)) == n  # every task claimed exactly once


async def test_claim_respects_dependencies(backend, tmp_path: Path):
    store = backend.task_store(tmp_path)
    a = await store.create(Task(objective="first"))
    b = await store.create(Task(objective="second", depends_on=[a.id]))
    # b is not claimable until a is done.
    first = await store.claim("w")
    assert first.id == a.id
    assert await store.claim("w") is None  # b blocked
    first.state = TaskState.done.value
    await store.update(first)
    second = await store.claim("w")
    assert second.id == b.id


async def test_stale_lease_reclaimed(backend, tmp_path: Path):
    store = backend.task_store(tmp_path)
    t = await store.create(Task(objective="x"))
    claimed = await store.claim("dead-owner", lease_ttl=-1)  # already expired
    assert claimed.id == t.id
    again = await store.claim("new-owner")
    assert again.id == t.id
    assert again.owner == "new-owner"


async def test_mailbox_send_inbox_read_purge(backend, tmp_path: Path):
    box = backend.mailbox_store(tmp_path)
    m = await box.send(Message(to="planner", body="hi", sender="human"))
    inbox = await box.inbox("planner")
    assert len(inbox) == 1 and inbox[0].body == "hi"
    assert len(await box.inbox("planner", unread_only=True)) == 1
    await box.mark_read("planner", m.id)
    assert len(await box.inbox("planner", unread_only=True)) == 0
    await box.purge("planner")
    assert await box.inbox("planner") == []


async def test_mailbox_hardlimit_trims(backend, tmp_path: Path):
    box = backend.mailbox_store(tmp_path, hard_limit=3)
    for i in range(5):
        await box.send(Message(to="a", body=str(i)))
    inbox = await box.inbox("a")
    assert [m.body for m in inbox] == ["2", "3", "4"]


async def test_mailbox_preserves_order(backend, tmp_path: Path):
    """At-least-once delivery preserves append order (CLI §13 / v2 §6)."""
    box = backend.mailbox_store(tmp_path)
    for i in range(5):
        await box.send(Message(to="a", body=str(i)))
    assert [m.body for m in await box.inbox("a")] == ["0", "1", "2", "3", "4"]


async def test_memory_put_get_query_gc(backend, tmp_path: Path):
    mem = backend.memory_store(tmp_path)
    await mem.put("team", "k1", "v1", tags=["x"])
    await mem.put("team", "k1", "v2", tags=["x"])  # latest wins
    assert await mem.get("team", "k1") == "v2"
    await mem.put("team", "k2", "v3", tags=["y"])
    assert len(await mem.query("team", tags=["x"])) == 2
    await mem.gc({"gcStrategy": "aggressive", "maxItems": 1})
    assert len(await mem.query("team")) == 1
