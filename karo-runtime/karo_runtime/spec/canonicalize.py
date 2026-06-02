"""Canonical JSON projection — the parity invariant (CLI §4.0.1, v2 §15).

"Byte-equivalent after canonicalization" is a CI-tested invariant. Two
independent YAML emitters (Python on the CLI, Go in the operator round-trip)
will never produce identical bytes, so parity is compared over a **canonical
JSON projection**, defined normatively here and treated as the single source of
truth for both lanes:

- keys sorted lexicographically at every level;
- integers rendered in plain decimal, no underscores, no exponent;
- inherited defaults are *materialized* into the projection, so two specs that
  differ only in what they left implicit compare equal;
- block-scalar ``instructions`` normalized to a plain string;
- UTF-8, normalized booleans/nulls, single trailing newline.

``metadata.namespace`` and the ``runtime:`` block live outside the shared
``spec`` body and are excluded from the parity comparison (CLI §12).
"""

from __future__ import annotations

import json
from typing import Any

from .models import AgentTeam, Defaults, Interaction


def _dump(model: Any) -> Any:
    """Serialize a pydantic model to its wire (aliased) dict form."""
    if model is None:
        return None
    return model.model_dump(by_alias=True, exclude_none=True, mode="json")


def materialized_spec(team: AgentTeam) -> dict[str, Any]:
    """Return the spec body with inherited defaults materialized per agent.

    Every agent ends up with an explicit ``harness``, ``model``,
    ``permissionMode`` and ``interaction`` (agent value → team defaults →
    built-in default), so the canonical projection of two specs that differ only
    in implicit-vs-explicit defaults is identical.
    """
    spec = team.spec
    defaults: Defaults = spec.defaults
    team_interaction: Interaction = spec.interaction

    spec_dict = _dump(spec)
    materialized_agents: list[dict[str, Any]] = []
    for agent in spec.agents:
        a = _dump(agent)
        a.setdefault("harness", (agent.harness or defaults.harness))
        if agent.model is not None:
            a["model"] = _dump(agent.model)
        elif defaults.model is not None:
            a["model"] = _dump(defaults.model)
        a.setdefault(
            "permissionMode", agent.permission_mode or defaults.permission_mode
        )
        if agent.interaction is not None:
            a["interaction"] = _dump(agent.interaction)
        else:
            a["interaction"] = _dump(team_interaction)
        if agent.mailbox is None:
            a["mailbox"] = agent.name
        materialized_agents.append(a)

    spec_dict["agents"] = materialized_agents
    return spec_dict


def canonical_json(obj: Any) -> str:
    """Render ``obj`` as canonical JSON (sorted keys, single trailing newline)."""
    return (
        json.dumps(
            obj,
            sort_keys=True,
            ensure_ascii=False,
            separators=(",", ":"),
            allow_nan=False,
        )
        + "\n"
    )


def parity_projection(team: AgentTeam) -> str:
    """Canonical JSON of the *shared spec body* used for parity comparison.

    Excludes ``metadata.namespace`` and ``runtime:`` (CLI §12); includes
    ``apiVersion``, ``kind``, ``metadata`` (minus namespace) and the
    defaults-materialized ``spec``.
    """
    meta = _dump(team.metadata)
    meta.pop("namespace", None)
    projection = {
        "apiVersion": team.api_version,
        "kind": team.kind,
        "metadata": meta,
        "spec": materialized_spec(team),
    }
    return canonical_json(projection)


def canonical_document(team: AgentTeam, *, include_runtime: bool = True) -> str:
    """Full canonical JSON of the document (for ``compile --format json``)."""
    doc = _dump(team)
    return canonical_json(doc)
