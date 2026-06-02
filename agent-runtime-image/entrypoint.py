#!/usr/bin/env python3
"""Agent-runtime pod entrypoint (PRD-KARO-v2.md §4.1).

A pod is parameterized entirely from env + the CRD. This entrypoint reads the
bootstrap contract, loads the agent's slice of the shared spec, and runs the
karo-runtime Coordinator loop for that **single** agent against the cluster
backends. The exact same karo-runtime code runs locally in the CLI — only the
store backends differ (file locally, Redis/Postgres on cluster).

Bootstrap env (v2 §4.1):
  KARO_TEAM, KARO_AGENT      which agent of which AgentTeam this pod is
  KARO_RUN_ID                the run this pod participates in
  KARO_TASKS_DSN             tasks backend (Postgres) DSN, from secretRef
  KARO_MEMORY_DSN            memory backend (Redis) DSN
  KARO_MAILBOX_DSN           mailbox backend (Redis) DSN
  KARO_SPEC_PATH             path to the projected spec (ConfigMap mount) or ""
  KARO_OTEL_ENDPOINT         tracing endpoint from runtime.observability
  ANTHROPIC_API_KEY / cloud creds via IRSA / Workload Identity (§8)
"""

from __future__ import annotations

import os
import sys


def require(name: str) -> str:
    val = os.environ.get(name)
    if not val:
        sys.exit(f"FATAL: required bootstrap env {name} is not set (v2 §4.1)")
    return val


def main() -> int:
    team_name = require("KARO_TEAM")
    agent_name = require("KARO_AGENT")
    run_id = os.environ.get("KARO_RUN_ID", "")
    spec_path = os.environ.get("KARO_SPEC_PATH", "")

    print(f"[agent-runtime] team={team_name} agent={agent_name} run={run_id}")

    # The shared library is the SAME code the CLI uses.
    import karo_runtime as kr

    print(f"[agent-runtime] karo_runtime {kr.__version__}, spec {kr.SPEC_API_VERSION}")

    if not spec_path or not os.path.exists(spec_path):
        print("[agent-runtime] no projected spec yet; idling. The controller will")
        print("[agent-runtime] mount the team spec and the Dispatcher will pump tasks.")
        # In a real pod this would block on the mailbox / task queue.
        return 0

    result = kr.compile_flat(spec_path)
    agent = next((a for a in result.team.spec.agents if a.name == agent_name), None)
    if agent is None:
        sys.exit(f"FATAL: agent {agent_name!r} not found in team {team_name!r}")

    # TODO(v2-M1/M2): construct Redis/Postgres stores implementing the
    # karo-runtime Protocols (KARO_TASKS_DSN/KARO_MEMORY_DSN/KARO_MAILBOX_DSN),
    # then run the Coordinator loop for this single agent. The file-backed CLI
    # path in karo_runtime.runtime.Coordinator is the reference behavior.
    print(f"[agent-runtime] loaded agent {agent_name!r}; ready to run the SDK adapter loop.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
