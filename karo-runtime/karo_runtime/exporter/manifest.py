"""``karo export`` → KARO v2 manifest (CLI §12, §4.6, §4.7, §4.2.1).

Emits the shared ``spec`` body (compiled, instructions inlined) plus a
``runtime:`` block of production defaults. Guarantees:

- the ``spec`` body is equivalent (after canonicalization, §4.0.1) to the
  compiled local spec — ``metadata.namespace`` and ``runtime:`` live outside the
  parity comparison;
- never emits resolved secret values — ``${secret:}``/``${env:}`` become
  ``secretRef``s in ``runtime.secrets`` (CLI §4.6, v2 §10);
- rejects (or warns on) non-cluster-capable harnesses (§4.7);
- coerces/rejects ``permissionMode: prompt`` for headless pods (§4.2.1).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from ..spec.canonicalize import materialized_spec
from ..spec.loader import find_interpolations
from ..spec.models import AgentTeam
from ..spec.validate import CLUSTER_CAPABLE_HARNESSES

# Runtime defaults by profile (CLI §12).
PROFILES: dict[str, dict[str, Any]] = {
    "staging": {
        "scaleToZero": True,
        "idleTimeoutSeconds": 120,
        "maxConcurrentAgents": 5,
        "observability": {
            "metrics": "victoriametrics",
            "tracing": {"exporter": "otel", "endpoint": "http://otel-collector:4317", "sampling": "sampled"},
        },
    },
    "prod": {
        "scaleToZero": True,
        "idleTimeoutSeconds": 300,
        "maxConcurrentAgents": 10,
        "observability": {
            "metrics": "victoriametrics",
            "tracing": {"exporter": "otel", "endpoint": "http://otel-collector:4317", "sampling": "full"},
        },
    },
}

DEFAULT_BACKENDS = {
    "memory": {"kind": "redis", "secretRef": {"name": "karo-redis"}},
    "mailbox": {"kind": "redis", "secretRef": {"name": "karo-redis"}},
    "tasks": {"kind": "postgres", "secretRef": {"name": "karo-pg"}},
}

DEFAULT_IMAGE = {"agentRuntime": "ghcr.io/karo/agent-runtime:v2", "pullPolicy": "IfNotPresent"}


class ExportError(Exception):
    pass


@dataclass
class ExportReport:
    transforms: list[str] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)
    secrets: dict[str, Any] = field(default_factory=dict)


def _gate_harnesses(team: AgentTeam, *, allow_local: bool, report: ExportReport) -> None:
    for a in team.spec.agents:
        harness = a.harness or team.spec.defaults.harness
        if harness not in CLUSTER_CAPABLE_HARNESSES:
            msg = (
                f"agent {a.name!r}: harness {harness!r} is local-only and cannot "
                f"run on cluster (only 'sdk' is cluster-capable; §4.7)"
            )
            if allow_local:
                report.warnings.append(msg)
            else:
                raise ExportError(msg + "; re-run with --allow-local-harness to warn instead")


def _coerce_permission_modes(team: AgentTeam, spec_dict: dict[str, Any], *, report: ExportReport) -> None:
    defaults = team.spec.defaults
    for agent_dict, agent in zip(spec_dict["agents"], team.spec.agents):
        pm = agent.permission_mode or defaults.permission_mode
        if pm == "prompt":
            # Coerce to a pauseBefore-style guard so the agent pauses for attach
            # rather than blocking on a dead TTY (§4.2.1).
            agent_dict["permissionMode"] = "plan"
            inter = agent_dict.setdefault("interaction", {})
            guards = inter.setdefault("guards", [])
            guards.append({"pauseBefore": ["Bash"]})
            report.transforms.append(
                f"agent {agent.name!r}: permissionMode 'prompt' coerced to 'plan' + "
                f"pauseBefore:[Bash] guard for headless operation (§4.2.1)"
            )


def _collect_secrets(team: AgentTeam, report: ExportReport) -> None:
    doc = team.model_dump(by_alias=True, exclude_none=True)
    for kind, arg in find_interpolations(doc):
        if kind == "secret":
            report.secrets[arg] = {"secretRef": {"name": arg, "key": "value"}}
        elif kind == "env":
            report.secrets[arg] = {"secretRef": {"name": arg.lower().replace("_", "-"), "key": "value"}}
        # ${file:} is inlined by the compiler/loader; nothing to collect here.


def export_manifest(
    team: AgentTeam,
    *,
    namespace: str | None = None,
    profile: str = "prod",
    allow_local_harness: bool = False,
    coerce_prompt: bool = True,
) -> tuple[dict[str, Any], ExportReport]:
    """Build the KARO v2 manifest document and an export report."""
    if profile not in PROFILES:
        raise ExportError(f"unknown profile {profile!r}; choose from {sorted(PROFILES)}")

    report = ExportReport()
    _gate_harnesses(team, allow_local=allow_local_harness, report=report)

    spec_dict = materialized_spec(team)
    if coerce_prompt:
        _coerce_permission_modes(team, spec_dict, report=report)
    _collect_secrets(team, report)

    meta = team.metadata.model_dump(by_alias=True, exclude_none=True)
    if namespace:
        meta["namespace"] = namespace

    runtime: dict[str, Any] = dict(PROFILES[profile])
    runtime["backends"] = dict(DEFAULT_BACKENDS)
    runtime["image"] = dict(DEFAULT_IMAGE)
    if report.secrets:
        runtime["secrets"] = report.secrets

    doc = {
        "apiVersion": team.api_version,
        "kind": "AgentTeam",
        "metadata": meta,
        "spec": spec_dict,
        "runtime": runtime,
    }
    return doc, report
