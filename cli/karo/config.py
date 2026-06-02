"""User config + credential profiles (CLI §14).

User config lives at ``~/.config/karo/config.yaml`` and is never committed. It
declares credential **profiles** and defaults.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import yaml

from karo_runtime.models import CredentialProfile


def config_path() -> Path:
    base = os.environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    return Path(base) / "karo" / "config.yaml"


def load_config() -> dict[str, Any]:
    p = config_path()
    if not p.exists():
        return {}
    data = yaml.safe_load(p.read_text("utf-8"))
    return data or {}


def load_profiles() -> dict[str, CredentialProfile]:
    cfg = load_config()
    out: dict[str, CredentialProfile] = {}
    for name, body in (cfg.get("profiles") or {}).items():
        provider = body.get("provider", "anthropic")
        out[name] = CredentialProfile(name=name, provider=provider, config=dict(body))
    return out
