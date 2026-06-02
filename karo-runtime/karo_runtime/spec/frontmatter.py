"""Parse YAML frontmatter + markdown body from ``AGENT.md`` / ``SKILL.md``.

Same convention Claude Code / Cursor / Codex use: a ``---`` delimited YAML
header followed by a markdown body. For an ``AGENT.md`` the body **is** the
agent's ``instructions`` (system prompt); the frontmatter carries the structured
fields (CLI §4.0).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

import yaml

_DELIM = "---"


@dataclass
class Frontmatter:
    data: dict[str, Any] = field(default_factory=dict)
    body: str = ""


def parse(text: str) -> Frontmatter:
    """Split frontmatter and body. Missing frontmatter yields empty ``data``."""
    stripped = text.lstrip("﻿")  # tolerate a BOM
    lines = stripped.splitlines()
    if not lines or lines[0].strip() != _DELIM:
        return Frontmatter(data={}, body=stripped.strip())

    # Find the closing delimiter.
    for idx in range(1, len(lines)):
        if lines[idx].strip() == _DELIM:
            header = "\n".join(lines[1:idx])
            body = "\n".join(lines[idx + 1 :])
            data = yaml.safe_load(header) if header.strip() else {}
            if data is None:
                data = {}
            if not isinstance(data, dict):
                raise ValueError("frontmatter must be a YAML mapping")
            return Frontmatter(data=data, body=body.strip())

    raise ValueError("unterminated frontmatter: missing closing '---'")
