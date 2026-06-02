"""YAML loading, include merging, interpolation and numeric-literal linting.

Interpolation (``${env:}`` / ``${secret:}`` / ``${file:}``) happens at
run/export time and is never persisted resolved (CLI §4.6). This module
provides the primitives; the CLI/exporter decide *when* to resolve.
"""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any, Callable

import yaml

# A YAML scalar that is purely an integer/float but contains a '_' grouping
# (e.g. ``limit: 5_000_000``). PyYAML (1.1) silently accepts it as an int while
# Go's yaml.v3 (1.2) parses it as a string — a parity break (CLI §4.0). We lint
# the *raw text* because by parse time the underscore is gone.
_NUMERIC_UNDERSCORE = re.compile(
    r":\s*[-+]?\d[\d_]*\d(?:\.\d+)?\s*(?:#.*)?$"
)

_INTERP = re.compile(r"\$\{(env|secret|file):([^}]+)\}")


class LoadError(Exception):
    def __init__(self, message: str, *, file: str = "", line: int = 0):
        super().__init__(message)
        self.message = message
        self.file = file
        self.line = line


def lint_numeric_literals(text: str, source: str) -> list[dict[str, Any]]:
    """Return diagnostics for ``_``-grouped numeric scalars (CLI §7)."""
    diags: list[dict[str, Any]] = []
    for i, line in enumerate(text.splitlines(), start=1):
        m = _NUMERIC_UNDERSCORE.search(line)
        if m and "_" in m.group(0):
            diags.append(
                {
                    "file": source,
                    "line": i,
                    "severity": "error",
                    "code": "numeric-underscore",
                    "message": (
                        "underscore grouping in a numeric scalar is not portable "
                        "(PyYAML reads it as int, Go yaml.v3 as a string); "
                        "write it in plain decimal"
                    ),
                }
            )
    return diags


def load_yaml(path: Path) -> dict[str, Any]:
    text = path.read_text(encoding="utf-8")
    data = yaml.safe_load(text)
    if data is None:
        return {}
    if not isinstance(data, dict):
        raise LoadError("top-level YAML must be a mapping", file=str(path))
    return data


def deep_merge(base: dict[str, Any], overlay: dict[str, Any]) -> dict[str, Any]:
    """Deep-merge ``overlay`` onto ``base``; ``overlay`` wins on scalars/lists."""
    out = dict(base)
    for key, val in overlay.items():
        if key in out and isinstance(out[key], dict) and isinstance(val, dict):
            out[key] = deep_merge(out[key], val)
        else:
            out[key] = val
    return out


def apply_includes(doc: dict[str, Any], base_dir: Path) -> dict[str, Any]:
    """Resolve top-level ``include:`` fragments; the local doc wins (CLI §4.8)."""
    includes = doc.get("include")
    if not includes:
        return doc
    merged: dict[str, Any] = {}
    for rel in includes:
        frag_path = (base_dir / rel).resolve()
        if not frag_path.exists():
            raise LoadError(f"include not found: {rel}", file=str(base_dir / rel))
        merged = deep_merge(merged, load_yaml(frag_path))
    local = {k: v for k, v in doc.items() if k != "include"}
    return deep_merge(merged, local)


# A resolver maps (kind, arg) -> str, e.g. ("env", "ANTHROPIC_API_KEY").
Resolver = Callable[[str, str], str]


def interpolate(value: Any, resolver: Resolver) -> Any:
    """Recursively resolve ``${kind:arg}`` placeholders using ``resolver``."""
    if isinstance(value, str):
        def _sub(m: re.Match[str]) -> str:
            return resolver(m.group(1), m.group(2).strip())

        return _INTERP.sub(_sub, value)
    if isinstance(value, dict):
        return {k: interpolate(v, resolver) for k, v in value.items()}
    if isinstance(value, list):
        return [interpolate(v, resolver) for v in value]
    return value


def find_interpolations(value: Any) -> list[tuple[str, str]]:
    """List every ``(kind, arg)`` placeholder found anywhere in ``value``."""
    found: list[tuple[str, str]] = []

    def _walk(v: Any) -> None:
        if isinstance(v, str):
            found.extend((m.group(1), m.group(2).strip()) for m in _INTERP.finditer(v))
        elif isinstance(v, dict):
            for x in v.values():
                _walk(x)
        elif isinstance(v, list):
            for x in v:
                _walk(x)

    _walk(value)
    return found
