"""Authoritative, synchronous token-budget meter (CLI §8, v2 §5.6).

Enforcement is a *check-and-reserve* against an atomic counter — a file-lock
guarded counter locally, a Redis ``INCRBY`` on cluster — never derived from
lagging observability metrics. This is the *same* ``karo-runtime`` code path on
both sides, so the ``onExceed`` decision is identical local and remote.
"""

from __future__ import annotations

import contextlib
import fcntl
import json
import os
import time
from dataclasses import dataclass
from enum import Enum
from pathlib import Path
from typing import Iterator, Optional


class OnExceed(str, Enum):
    warn = "warn"
    pause = "pause"
    hardstop = "hardstop"


@dataclass
class SpendDecision:
    allowed: bool
    used: int
    limit: Optional[int]
    mode: str  # warn | pause | hardstop | ok


@contextlib.contextmanager
def _flock(path: Path) -> Iterator[None]:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(path), os.O_CREAT | os.O_RDWR, 0o644)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        yield
    finally:
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)


class FileBudgetMeter:
    """File-lock-guarded atomic counter (the local equivalent of Redis INCRBY).

    The counter file holds ``{provider: {used, window_start}}``. ``can_spend``
    reserves ``est`` tokens up front so N concurrent processes can't overshoot
    the limit before a later reconciliation notices.
    """

    def __init__(
        self,
        path: str | Path = ".karo/usage.json",
        *,
        limit: Optional[int] = None,
        window: str = "daily",
        on_exceed: str = OnExceed.pause.value,
        usage_log: Optional[str | Path] = None,
    ):
        self.path = Path(path)
        self.lock = self.path.with_suffix(".lock")
        self.limit = limit
        self.window = window
        self.on_exceed = on_exceed
        self.usage_log = Path(usage_log) if usage_log else self.path.parent / "usage.log"

    def _load(self) -> dict:
        if self.path.exists():
            return json.loads(self.path.read_text("utf-8"))
        return {}

    def _save(self, data: dict) -> None:
        tmp = self.path.with_suffix(".tmp")
        tmp.parent.mkdir(parents=True, exist_ok=True)
        tmp.write_text(json.dumps(data), encoding="utf-8")
        os.replace(tmp, self.path)

    def _window_start(self) -> float:
        now = time.time()
        if self.window == "daily":
            # 00:00 UTC of the current day.
            return now - (now % 86400)
        if self.window == "session":
            return 0.0  # session windows reset per run via reset()
        return 0.0  # unbounded

    def _rollover(self, entry: dict) -> dict:
        if self.window == "daily":
            start = self._window_start()
            if entry.get("window_start", 0) < start:
                return {"used": 0, "window_start": start}
        return entry

    def can_spend(self, provider: str, est: int, agent: str = "") -> SpendDecision:
        """Check-and-reserve ``est`` tokens for ``provider``. Authoritative."""
        if self.limit is None:
            self.record(provider, agent, est, 0)
            return SpendDecision(True, 0, None, "ok")
        with _flock(self.lock):
            data = self._load()
            entry = self._rollover(data.get(provider, {"used": 0, "window_start": self._window_start()}))
            projected = entry["used"] + est
            if projected > self.limit:
                # Over budget: do not reserve. The caller applies onExceed.
                return SpendDecision(False, entry["used"], self.limit, self.on_exceed)
            entry["used"] = projected
            data[provider] = entry
            self._save(data)
            return SpendDecision(True, entry["used"], self.limit, "ok")

    def record(self, provider: str, agent: str, prompt_tokens: int, completion_tokens: int, estimated: bool = False) -> None:
        """Append a usage record to ``usage.log`` (observability, not authority)."""
        self.usage_log.parent.mkdir(parents=True, exist_ok=True)
        with self.usage_log.open("a", encoding="utf-8") as fh:
            fh.write(
                json.dumps(
                    {
                        "ts": time.time(),
                        "provider": provider,
                        "agent": agent,
                        "prompt_tokens": prompt_tokens,
                        "completion_tokens": completion_tokens,
                        "estimated": estimated,
                    }
                )
                + "\n"
            )

    def status(self, provider: str) -> dict:
        with _flock(self.lock):
            data = self._load()
            entry = self._rollover(data.get(provider, {"used": 0}))
            return {
                "provider": provider,
                "used": entry.get("used", 0),
                "limit": self.limit,
                "window": self.window,
            }

    def reset(self) -> None:
        with _flock(self.lock):
            self._save({})
