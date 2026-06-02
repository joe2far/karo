"""Spec layer: models, compiler, loader, validator, canonicalizer, schema.

This subpackage is the contract shared verbatim between the CLI and the operator
(via ``karo-runtime``). Do not diverge it.
"""

from .canonicalize import canonical_json, materialized_spec, parity_projection
from .compile import CompileResult, compile_flat, compile_folder, compile_path
from .models import AgentTeam, AgentTeamSpec, ACCEPTED_API_VERSIONS
from .schema_export import builtin_defaults, json_schema
from .validate import Diagnostic, validate

__all__ = [
    "AgentTeam",
    "AgentTeamSpec",
    "ACCEPTED_API_VERSIONS",
    "CompileResult",
    "compile_folder",
    "compile_flat",
    "compile_path",
    "canonical_json",
    "materialized_spec",
    "parity_projection",
    "json_schema",
    "builtin_defaults",
    "validate",
    "Diagnostic",
]
