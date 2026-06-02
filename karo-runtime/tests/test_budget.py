"""Authoritative budget meter tests — no overspend under concurrency (§8)."""

import asyncio
from pathlib import Path

from karo_runtime.runtime.budget import FileBudgetMeter, OnExceed


def test_reserve_blocks_over_limit(tmp_path: Path):
    meter = FileBudgetMeter(tmp_path / "usage.json", limit=100, window="session")
    assert meter.can_spend("anthropic", 60).allowed
    assert meter.can_spend("anthropic", 30).allowed
    decision = meter.can_spend("anthropic", 30)  # would hit 120 > 100
    assert not decision.allowed
    assert decision.used == 90
    assert decision.limit == 100


def test_unlimited_always_allows(tmp_path: Path):
    meter = FileBudgetMeter(tmp_path / "usage.json", limit=None)
    assert meter.can_spend("anthropic", 10_000_000).allowed


async def test_no_overspend_under_concurrency(tmp_path: Path):
    """N concurrent reservations must not exceed the limit (atomic counter)."""
    limit = 100
    meter = FileBudgetMeter(tmp_path / "usage.json", limit=limit, window="session")

    async def spend() -> bool:
        return await asyncio.to_thread(lambda: meter.can_spend("anthropic", 10).allowed)

    results = await asyncio.gather(*[spend() for _ in range(50)])
    allowed = sum(results)
    # at most limit/10 = 10 reservations may succeed
    assert allowed <= limit // 10
    assert meter.status("anthropic")["used"] <= limit


def test_reset_clears_window(tmp_path: Path):
    meter = FileBudgetMeter(tmp_path / "usage.json", limit=100, window="session")
    meter.can_spend("anthropic", 50)
    assert meter.status("anthropic")["used"] == 50
    meter.reset()
    assert meter.status("anthropic")["used"] == 0


def test_onexceed_mode_surfaced(tmp_path: Path):
    meter = FileBudgetMeter(
        tmp_path / "usage.json", limit=10, window="session", on_exceed=OnExceed.hardstop.value
    )
    meter.can_spend("anthropic", 10)
    d = meter.can_spend("anthropic", 5)
    assert d.mode == "hardstop"
