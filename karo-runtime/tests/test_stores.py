"""Store conformance contract — runs against every backend, per store type.

This IS the shared contract suite the design relies on (CLI §11, v2 §6/§13): the
file backend runs it locally; Postgres (tasks) and Redis (mailbox/memory) run the
*same* assertions on cluster — including the atomic-claim concurrency test, the
core swarm safety guarantee. Cluster backends skip when their DSN env var / driver
is absent (see store_contract docstring).
"""

import asyncio
from pathlib import Path

import pytest

from karo_runtime.stores.base import Message, Task, TaskState

from store_contract import (
    MAILBOX_BACKENDS,
    MEMORY_BACKENDS,
    TASK_BACKENDS,
    maybe_skip,
)


@pytest.fixture(params=TASK_BACKENDS, ids=lambda b: b.name)
def task_store(request, tmp_path: Path):
    if (reason := maybe_skip(request.param)):
        pytest.skip(reason)
    return request.param.make(tmp_path)


@pytest.fixture(params=MAILBOX_BACKENDS, ids=lambda b: b.name)
def mailbox(request, tmp_path: Path):
    if (reason := maybe_skip(request.param)):
        pytest.skip(reason)
    return lambda hard_limit=500: request.param.make(tmp_path, hard_limit=hard_limit)


@pytest.fixture(params=MEMORY_BACKENDS, ids=lambda b: b.name)
def memory(request, tmp_path: Path):
    if (reason := maybe_skip(request.param)):
        pytest.skip(reason)
    return request.param.make(tmp_path)


# ---- task store contract -------------------------------------------------- #
async def test_task_crud_and_transition(task_store):
    t = await task_store.create(Task(objective="do it"))
    assert (await task_store.get(t.id)).objective == "do it"
    t.state = TaskState.done.value
    await task_store.update(t)
    assert (await task_store.get(t.id)).state == "done"
    assert len(await task_store.list(state="done")) == 1


async def test_atomic_claim_no_double_execution(task_store):
    """No two claimants ever get the same task (the core swarm guarantee)."""
    n = 25
    for _ in range(n):
        await task_store.create(Task(objective="work"))

    async def worker(name: str) -> list[str]:
        claimed = []
        while True:
            t = await task_store.claim(owner=name)
            if t is None:
                break
            claimed.append(t.id)
        return claimed

    results = await asyncio.gather(*[worker(f"w{i}") for i in range(8)])
    flat = [tid for r in results for tid in r]
    assert len(flat) == n
    assert len(set(flat)) == n  # every task claimed exactly once


async def test_claim_respects_dependencies(task_store):
    a = await task_store.create(Task(objective="first"))
    b = await task_store.create(Task(objective="second", depends_on=[a.id]))
    first = await task_store.claim("w")
    assert first.id == a.id
    assert await task_store.claim("w") is None  # b blocked until a done
    first.state = TaskState.done.value
    await task_store.update(first)
    second = await task_store.claim("w")
    assert second.id == b.id


async def test_stale_lease_reclaimed(task_store):
    t = await task_store.create(Task(objective="x"))
    claimed = await task_store.claim("dead-owner", lease_ttl=-1)  # already expired
    assert claimed.id == t.id
    again = await task_store.claim("new-owner")
    assert again.id == t.id
    assert again.owner == "new-owner"


# ---- mailbox contract ----------------------------------------------------- #
async def test_mailbox_send_inbox_read_purge(mailbox):
    box = mailbox()
    m = await box.send(Message(to="planner", body="hi", sender="human"))
    inbox = await box.inbox("planner")
    assert len(inbox) == 1 and inbox[0].body == "hi"
    assert len(await box.inbox("planner", unread_only=True)) == 1
    await box.mark_read("planner", m.id)
    assert len(await box.inbox("planner", unread_only=True)) == 0
    await box.purge("planner")
    assert await box.inbox("planner") == []


async def test_mailbox_hardlimit_trims(mailbox):
    box = mailbox(hard_limit=3)
    for i in range(5):
        await box.send(Message(to="a", body=str(i)))
    assert [m.body for m in await box.inbox("a")] == ["2", "3", "4"]


async def test_mailbox_preserves_order(mailbox):
    """At-least-once delivery preserves append order (CLI §13 / v2 §6)."""
    box = mailbox()
    for i in range(5):
        await box.send(Message(to="a", body=str(i)))
    assert [m.body for m in await box.inbox("a")] == ["0", "1", "2", "3", "4"]


# ---- memory contract ------------------------------------------------------ #
async def test_memory_put_get_query_gc(memory):
    await memory.put("team", "k1", "v1", tags=["x"])
    await memory.put("team", "k1", "v2", tags=["x"])  # latest wins
    assert await memory.get("team", "k1") == "v2"
    await memory.put("team", "k2", "v3", tags=["y"])
    assert len(await memory.query("team", tags=["x"])) == 2
    await memory.gc({"gcStrategy": "aggressive", "maxItems": 1})
    assert len(await memory.query("team")) == 1
