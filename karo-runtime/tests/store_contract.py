"""Backend-agnostic store conformance harness (CLI §11, v2 §6/§13/§15).

The store *contract* is defined once in ``test_stores.py`` and runs against every
registered backend, **per store type**:

- ``TASK_BACKENDS``    — file (local) + postgres (cluster)
- ``MAILBOX_BACKENDS`` — file (local) + redis (cluster)
- ``MEMORY_BACKENDS``  — file (local) + redis (cluster)

The cluster backends pass the *same* assertions as the file backend — including
the atomic-claim concurrency test — which is what makes "file locally,
redis+postgres on cluster" a tested invariant. They are skipped (not failed) when
their driver isn't installed or the server DSN env var isn't set, so this runs
green locally and is exercised for real on the user's kind cluster:

    KARO_TEST_REDIS_URL=redis://localhost:6379/0 \
    KARO_TEST_PG_DSN=postgresql://karo:karo@localhost:5432/karo \
    pytest karo-runtime/tests/test_stores.py
"""

from __future__ import annotations

import importlib.util
import os
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Optional

from karo_runtime.stores.file import (
    FileMailboxStore,
    FileMemoryStore,
    FileTaskStore,
)


def _have(mod: str) -> bool:
    return importlib.util.find_spec(mod) is not None


def _ns(tmp_path: Path) -> str:
    """A unique, SQL/redis-safe namespace per test (isolates parallel runs)."""
    return "karo_" + re.sub(r"[^a-zA-Z0-9]", "_", tmp_path.name)[-40:]


@dataclass
class Backend:
    name: str
    make: Callable[..., Any]
    available: bool = True
    skip_reason: str = ""


# ---- task stores ---------------------------------------------------------- #
_PG_DSN = os.environ.get("KARO_TEST_PG_DSN")


def _file_task(tmp_path: Path):
    return FileTaskStore(tmp_path / "tasks")


def _pg_task(tmp_path: Path):
    from karo_runtime.stores.postgres import PostgresTaskStore

    return PostgresTaskStore(_PG_DSN, table=_ns(tmp_path))


TASK_BACKENDS = [
    Backend("file", _file_task),
    Backend(
        "postgres", _pg_task,
        available=bool(_PG_DSN) and _have("asyncpg"),
        skip_reason="set KARO_TEST_PG_DSN and `pip install karo-runtime[postgres]`",
    ),
]


# ---- mailbox stores ------------------------------------------------------- #
# Postgres is the DEFAULT cluster backend for mailbox + memory too (one stateful
# dependency for the whole cluster). Redis stays available in stores/redis.py and
# can be wired in later, but is not part of the default contract run.
def _file_mail(tmp_path: Path, hard_limit: int = 500):
    return FileMailboxStore(tmp_path / "mail", hard_limit=hard_limit)


def _pg_mail(tmp_path: Path, hard_limit: int = 500):
    from karo_runtime.stores.postgres import PostgresMailboxStore

    return PostgresMailboxStore(_PG_DSN, hard_limit=hard_limit, table=_ns(tmp_path) + "_mail")


MAILBOX_BACKENDS = [
    Backend("file", _file_mail),
    Backend(
        "postgres", _pg_mail,
        available=bool(_PG_DSN) and _have("asyncpg"),
        skip_reason="set KARO_TEST_PG_DSN and `pip install karo-runtime[postgres]`",
    ),
]


# ---- memory stores -------------------------------------------------------- #
def _file_mem(tmp_path: Path):
    return FileMemoryStore(tmp_path / "mem")


def _pg_mem(tmp_path: Path):
    from karo_runtime.stores.postgres import PostgresMemoryStore

    return PostgresMemoryStore(_PG_DSN, table=_ns(tmp_path) + "_mem")


MEMORY_BACKENDS = [
    Backend("file", _file_mem),
    Backend(
        "postgres", _pg_mem,
        available=bool(_PG_DSN) and _have("asyncpg"),
        skip_reason="set KARO_TEST_PG_DSN and `pip install karo-runtime[postgres]`",
    ),
]


def maybe_skip(backend: Backend) -> Optional[str]:
    return None if backend.available else (backend.skip_reason or f"{backend.name} unavailable")
