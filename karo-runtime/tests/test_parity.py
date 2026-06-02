"""Parity Checkpoint A — the headline tested invariant (CLI §19, v2 §15).

"The same AgentTeam runs locally and on Kubernetes." This stands the invariant up
*now*, at M1, even though it cannot fully pass until M2 coordination lands — that
is the point: it encodes the contract M2 must meet, in CI, from day one.

Two halves:

1. SPEC parity (PASSES now): the compiled spec's canonical projection is identical
   regardless of how it was authored/where it runs (the wire-format invariant).
2. RUNTIME parity (the M2 target): running the SAME fixture with a deterministic
   harness must produce an IDENTICAL task graph + outputs locally (file stores)
   and on cluster (redis/postgres). This is skipped/xfail until the M2 items in
   ``M2_REQUIREMENTS`` are delivered.

Run the runtime half against real backends with:

    KARO_TEST_REDIS_URL=redis://localhost:6379/0 \
    KARO_TEST_PG_DSN=postgresql://karo:karo@localhost:5432/karo \
    pytest karo-runtime/tests/test_parity.py
"""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from karo_runtime.runtime import Coordinator
from karo_runtime.spec import compile_flat, parity_projection

FIXTURE = Path(__file__).parent / "fixtures" / "parity_team.yaml"

# Exactly what M2 must deliver to make the runtime parity assertion pass.
M2_REQUIREMENTS = [
    "Coordinator drives lead-and-teammates with real lead task decomposition "
    "(not one-task-per-teammate planning).",
    "Atomic task claim wired into the run loop (Coordinator calls store.claim(), "
    "not the non-atomic _next_runnable scan) so file and postgres agree.",
    "Mailbox-mediated handoff between agents (lead -> teammates -> lead) with "
    "deterministic ordering.",
    "Resume parity: kill mid-run and resume from committed state yields the same "
    "terminal task graph on both backends.",
    "Cluster execution path (operator Dispatcher + agent pods) produces the same "
    "graph as local; requires KARO_TEST_REDIS_URL + KARO_TEST_PG_DSN (or kind).",
]


def _signature(team, project_dir: Path, *, tasks=None, mailbox=None, memory=None) -> list[dict]:
    """A backend-independent, deterministic signature of a run's task graph.

    Tasks are keyed by (owner, objective) and dependencies are projected to those
    keys (not ids), so two runs on different stores compare equal iff the graph +
    outputs match.
    """
    coord = Coordinator(team, project_dir=project_dir, dry_run=False)
    if tasks is not None:
        coord.tasks = tasks
    if mailbox is not None:
        coord.mail = mailbox
    if memory is not None:
        coord.memory = memory
    import asyncio

    res = asyncio.run(coord.run("Ship a small feature end to end."))
    by_id = {t.id: t for t in res.tasks}

    def key(t):
        return f"{t.owner}:{t.objective}"

    rows = [
        {
            "key": key(t),
            "state": t.state,
            "deps": sorted(key(by_id[d]) for d in t.depends_on if d in by_id),
            "result": (t.result or {}).get("text", "") if t.result else "",
        }
        for t in res.tasks
    ]
    return sorted(rows, key=lambda d: d["key"])


def test_spec_parity_projection_is_stable():
    """SPEC half of parity (passes now): canonical projection is deterministic
    and excludes namespace/runtime, so local and cluster compare the same body."""
    team = compile_flat(FIXTURE).team
    a = parity_projection(team)
    b = parity_projection(compile_flat(FIXTURE).team)
    assert a == b
    # Large integer rendered plain-decimal (the YAML-portability invariant).
    assert "5000000" in a and "5_000_000" not in a


def test_local_run_is_deterministic(tmp_path):
    """Same fixture, two local file-store runs → identical task-graph signature.
    (Determinism is the precondition for cross-backend parity.)"""
    team = compile_flat(FIXTURE).team
    sig1 = _signature(team, tmp_path / "run1")
    sig2 = _signature(team, tmp_path / "run2")
    assert sig1 == sig2


def test_parity_checkpoint_a_runtime(tmp_path):
    """RUNTIME parity: local (file) == cluster (Postgres) task graph + outputs.

    M2 delivered the requirements in ``M2_REQUIREMENTS`` (atomic claim in the run
    loop, lead decomposition + mailbox handoff, review state, resume), so this now
    passes for real when ``KARO_TEST_PG_DSN`` is set (e.g. on kind); it skips
    cleanly without a Postgres DSN."""
    pg_dsn = os.environ.get("KARO_TEST_PG_DSN")
    if not pg_dsn:
        pytest.skip(
            "cluster backend not configured (set KARO_TEST_PG_DSN to run Parity "
            "Checkpoint A against Postgres; e.g. on kind)."
        )

    from karo_runtime.stores.postgres import (
        PostgresMailboxStore,
        PostgresMemoryStore,
        PostgresTaskStore,
    )

    import uuid

    sfx = uuid.uuid4().hex[:8]  # fresh tables per run (PG tables persist)
    team = compile_flat(FIXTURE).team
    local = _signature(team, tmp_path / "local")
    cluster = _signature(
        team,
        tmp_path / "cluster",
        tasks=PostgresTaskStore(pg_dsn, table=f"parity_tasks_{sfx}"),
        mailbox=PostgresMailboxStore(pg_dsn, table=f"parity_mail_{sfx}"),
        memory=PostgresMemoryStore(pg_dsn, table=f"parity_mem_{sfx}"),
    )
    assert local == cluster, "local and cluster task graphs diverged — parity broken"
