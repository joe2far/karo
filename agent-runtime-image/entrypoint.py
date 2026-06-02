#!/usr/bin/env python3
"""Agent-runtime pod entrypoint (PRD-KARO-v2.md §4.1).

A pod is parameterized entirely from env + the CRD. This entrypoint reads the
bootstrap contract, loads the agent's slice of the shared spec, and runs the
karo-runtime Coordinator loop against the cluster backends. The exact same
karo-runtime code runs locally in the CLI — only the store backends differ
(file locally, Redis/Postgres on cluster).

Bootstrap env (v2 §4.1):
  KARO_TEAM, KARO_AGENT      which agent of which AgentTeam this pod is
  KARO_RUN_ID                the run this pod participates in
  KARO_TASKS_DSN             tasks backend (Postgres) DSN, from secretRef
  KARO_MEMORY_DSN            memory backend (Redis) DSN
  KARO_MAILBOX_DSN           mailbox backend (Redis) DSN
  KARO_SPEC_PATH             path to the projected spec (ConfigMap mount) or ""
  KARO_OBJECTIVE             the objective to drive the coordinator with (or "")
  KARO_OTEL_ENDPOINT         tracing endpoint from runtime.observability
  ANTHROPIC_API_KEY / cloud creds via IRSA / Workload Identity (§8)
"""

from __future__ import annotations

import asyncio
import os
import sys


def require(name: str) -> str:
    val = os.environ.get(name)
    if not val:
        sys.exit(f"FATAL: required bootstrap env {name} is not set (v2 §4.1)")
    return val


def _status(msg: str) -> None:
    """Emit a structured, greppable status line."""
    print(f"[agent-runtime] {msg}", flush=True)


def _inject_cluster_stores(coord) -> bool:
    """Replace the Coordinator's file stores with cluster stores built from the
    backend DSNs (v2 §4.1). Returns True if any DSN-backed store was injected.

    The Redis/Postgres stores implement the SAME karo_runtime store Protocols as
    the file stores; we just swap the attributes the Coordinator reads
    (coord.tasks / coord.memory / coord.mail). Imports are local + guarded so a
    missing DSN, or a missing optional driver (asyncpg / redis), degrades to the
    file path instead of crashing at import time.
    """
    injected = False

    tasks_dsn = os.environ.get("KARO_TASKS_DSN", "")
    if tasks_dsn:
        try:
            from karo_runtime.stores.postgres import PostgresTaskStore

            coord.tasks = PostgresTaskStore(tasks_dsn)
            _status("tasks store: PostgresTaskStore (KARO_TASKS_DSN)")
            injected = True
        except Exception as exc:  # noqa: BLE001 - degrade gracefully
            _status(f"tasks store: KARO_TASKS_DSN set but Postgres unavailable ({exc}); using file store")

    memory_dsn = os.environ.get("KARO_MEMORY_DSN", "")
    if memory_dsn:
        try:
            from karo_runtime.stores.postgres import PostgresMemoryStore

            coord.memory = PostgresMemoryStore(memory_dsn)
            _status("memory store: PostgresMemoryStore (KARO_MEMORY_DSN)")
            injected = True
        except Exception as exc:  # noqa: BLE001 - degrade gracefully
            _status(f"memory store: KARO_MEMORY_DSN set but Postgres unavailable ({exc}); using file store")

    mailbox_dsn = os.environ.get("KARO_MAILBOX_DSN", "")
    if mailbox_dsn:
        try:
            from karo_runtime.stores.postgres import PostgresMailboxStore

            coord.mail = PostgresMailboxStore(mailbox_dsn)
            _status("mailbox store: PostgresMailboxStore (KARO_MAILBOX_DSN)")
            injected = True
        except Exception as exc:  # noqa: BLE001 - degrade gracefully
            _status(f"mailbox store: KARO_MAILBOX_DSN set but Postgres unavailable ({exc}); using file store")

    return injected


def main() -> int:
    team_name = require("KARO_TEAM")
    agent_name = require("KARO_AGENT")
    run_id = os.environ.get("KARO_RUN_ID", "") or None
    spec_path = os.environ.get("KARO_SPEC_PATH", "") or "/etc/karo/agent.json"
    objective = os.environ.get("KARO_OBJECTIVE", "")

    _status(f"team={team_name} agent={agent_name} run={run_id or '-'}")

    # The shared library is the SAME code the CLI uses.
    import karo_runtime as kr

    _status(f"karo_runtime {kr.__version__}, spec {kr.SPEC_API_VERSION}")

    if not spec_path or not os.path.exists(spec_path):
        _status("no projected spec yet; idling. The controller will mount the")
        _status("team spec and the Dispatcher will pump tasks.")
        # In a real pod this would block on the mailbox / task queue.
        return 0

    result = kr.compile_flat(spec_path)
    team = result.team
    agent = next((a for a in team.spec.agents if a.name == agent_name), None)
    if agent is None:
        _status(f"agent {agent_name!r} not in projected spec; idling")
        return 0
    _status(f"loaded agent {agent_name!r} from {spec_path}")

    from karo_runtime.runtime.coordinator import Coordinator

    # agent=agent_name scopes this pod to its own work and ensures only the lead
    # pod plans — N pods sharing one Postgres never duplicate the task graph.
    coord = Coordinator(team, run_id=run_id, agent=agent_name)

    # Swap in the cluster stores when DSNs are present; otherwise the Coordinator
    # keeps its default file stores (identical Protocols, v2 §4.2).
    if _inject_cluster_stores(coord):
        _status("running against cluster backends")
    else:
        _status("no backend DSNs set; running against file stores")

    if not objective:
        _status("no KARO_OBJECTIVE set; idling (awaiting Dispatcher).")
        return 0

    _status(f"running coordinator for objective: {objective!r}")
    run_result = asyncio.run(coord.run(objective))
    _status(
        f"run {run_result.run_id} done: completed={run_result.completed} "
        f"halted={run_result.halted} reason={run_result.halt_reason or '-'}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
