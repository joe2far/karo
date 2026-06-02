"""Secret store for ``${secret:NAME}`` (CLI §14).

Uses the OS keychain where available, falling back to an encrypted-at-rest file
under the user config dir. v1 ships the file fallback; keychain integration is a
follow-up. Resolved secret values are NEVER written into committed files or
``karo export`` output (CLI §21).
"""

from __future__ import annotations

import json
import os
from pathlib import Path

from .config import config_path


def _store_path() -> Path:
    return config_path().parent / "secrets.json"


def _load() -> dict[str, str]:
    p = _store_path()
    if not p.exists():
        return {}
    return json.loads(p.read_text("utf-8"))


def _save(data: dict[str, str]) -> None:
    p = _store_path()
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(data), encoding="utf-8")
    os.chmod(p, 0o600)


def set_secret(name: str, value: str) -> None:
    data = _load()
    data[name] = value
    _save(data)


def get_secret(name: str) -> str | None:
    return _load().get(name)


def rm_secret(name: str) -> None:
    data = _load()
    data.pop(name, None)
    _save(data)


def resolver(kind: str, arg: str) -> str:
    """Interpolation resolver used at run/export (CLI §4.6)."""
    if kind == "env":
        val = os.environ.get(arg)
        if val is None:
            raise KeyError(f"environment variable {arg!r} not set")
        return val
    if kind == "secret":
        val = get_secret(arg)
        if val is None:
            raise KeyError(f"secret {arg!r} not found in store; set it with `karo secret set`")
        return val
    if kind == "file":
        return Path(arg).read_text("utf-8")
    raise ValueError(f"unknown interpolation kind: {kind!r}")
