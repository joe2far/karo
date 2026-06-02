#!/usr/bin/env python3
"""Regenerate the committed AgentTeam JSON Schema (the parity source of truth).

Usage: python scripts/gen_schema.py

Writes ``karo-runtime/schema/agentteam.schema.json`` from the pydantic models so
the CI drift check (generated == committed) stays green. Run this after any
change to ``karo_runtime/spec/models.py`` or ``schema_export.py``.
"""
from __future__ import annotations

import json
import pathlib

import karo_runtime as kr

OUT = pathlib.Path(__file__).resolve().parents[1] / "karo-runtime" / "schema" / "agentteam.schema.json"


def main() -> None:
    text = json.dumps(kr.json_schema(), indent=2, sort_keys=True) + "\n"
    OUT.write_text(text, encoding="utf-8")
    print(f"wrote {OUT} ({len(text)} bytes)")


if __name__ == "__main__":
    main()
