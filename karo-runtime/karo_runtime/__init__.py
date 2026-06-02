"""karo-runtime — the shared library imported by the KARO CLI and the operator's
agent-runtime image (CLI §17, v2 §4.2/§13).

It houses the behavioral contract that must be identical on the laptop and on
the cluster: the spec models + compiler + canonicalizer + validator + JSON
Schema, the store Protocols, the harness-adapter interface, the model router,
the authoritative budget meter, the Coordinator, and the export transform.

Backends are the only per-lane difference: the CLI uses the file stores here;
the operator supplies Redis/Postgres stores implementing the same Protocols.
"""

from __future__ import annotations

__version__ = "0.1.0"
SPEC_API_VERSION = "karo.dev/v1"

from .spec import (  # noqa: E402
    AgentTeam,
    AgentTeamSpec,
    CompileResult,
    Diagnostic,
    canonical_json,
    compile_flat,
    compile_folder,
    compile_path,
    builtin_defaults,
    json_schema,
    materialized_spec,
    parity_projection,
    validate,
)

__all__ = [
    "__version__",
    "SPEC_API_VERSION",
    "AgentTeam",
    "AgentTeamSpec",
    "CompileResult",
    "Diagnostic",
    "compile_folder",
    "compile_flat",
    "compile_path",
    "validate",
    "json_schema",
    "builtin_defaults",
    "canonical_json",
    "materialized_spec",
    "parity_projection",
]
