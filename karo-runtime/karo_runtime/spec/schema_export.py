"""JSON Schema generation — the single source of truth shared with the operator.

CI generates/validates the Go ``AgentTeam`` types and the pydantic models
against this schema, failing on drift (CLI §4.4, v2 §16). Editors consume it for
validation.
"""

from __future__ import annotations

from typing import Any

from .models import AgentTeam

# Built-in defaults surfaced by ``karo schema --defaults`` (CLI §4.5).
BUILTIN_DEFAULTS: dict[str, Any] = {
    "spec.defaults.harness": "sdk",
    "spec.defaults.model.provider": "anthropic",
    "spec.defaults.permissionMode": "prompt",
    "spec.defaults.workingDir": "./workspace",
    "spec.budgets.team.window": "daily",
    "spec.budgets.team.onExceed": "pause",
    "spec.budgets.perAgent": False,
    "spec.memory.backend": "file",
    "spec.memory.scope": "team",
    "spec.memory.retention.gcStrategy": "lru",
    "spec.coordination.pattern": "lead-and-teammates",
    "spec.coordination.mailbox.backend": "file",
    "spec.coordination.mailbox.hardLimit": 500,
    "spec.coordination.taskLayer.backend": "file",
    "spec.coordination.taskLayer.resumable": True,
    "spec.interaction.attachable": True,
    "spec.interaction.autonomy": "supervised",
    "spec.interaction.pauseTimeout": 0,
    "agent.mailbox": "<agent name>",
}


def _inject_conditional_rules(schema: dict[str, Any]) -> None:
    """Express the §4.3 cross-field rules as JSON-Schema ``if/then`` so the
    operator's CRD validation can agree with ``karo validate`` (CLI §4.3, §4.4).

    pydantic v2 does not emit conditional schemas, so we add them to the
    ``Coordination`` definition post-generation:

    - ``pattern == lead-and-teammates`` ⇒ ``lead`` required;
    - ``pattern == pipeline``           ⇒ ``pipeline`` required.

    (Reference-resolution and prompt+autonomous rules are not expressible as
    pure if/then on a single object and remain in ``validate.py`` and the Go
    controller's ``validateTeam``.)
    """
    defs = schema.get("$defs", {})
    coord = defs.get("Coordination")
    if not coord:
        return
    coord.setdefault("allOf", [])
    coord["allOf"].extend(
        [
            {
                "if": {"properties": {"pattern": {"const": "lead-and-teammates"}}},
                "then": {"required": ["lead"]},
            },
            {
                "if": {"properties": {"pattern": {"const": "pipeline"}}},
                "then": {"required": ["pipeline"]},
            },
        ]
    )


def json_schema() -> dict[str, Any]:
    """Return the JSON Schema for the compiled ``AgentTeam`` document."""
    schema = AgentTeam.model_json_schema(by_alias=True)
    schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
    schema["$id"] = "https://karo.dev/schema/v1/agentteam.json"
    schema["title"] = "AgentTeam"
    _inject_conditional_rules(schema)
    return schema


def builtin_defaults() -> dict[str, Any]:
    return dict(BUILTIN_DEFAULTS)
