"""Loader unit tests: include deep-merge (§4.8) and lazy interpolation (§4.6)."""

from pathlib import Path

import pytest

from karo_runtime.spec.loader import (
    LoadError,
    apply_includes,
    deep_merge,
    find_interpolations,
    interpolate,
)


def test_deep_merge_local_wins_on_scalars_and_lists():
    base = {"a": 1, "nested": {"x": 1, "y": 2}, "lst": [1, 2]}
    overlay = {"a": 9, "nested": {"y": 20, "z": 30}, "lst": [9]}
    out = deep_merge(base, overlay)
    assert out["a"] == 9  # scalar override
    assert out["nested"] == {"x": 1, "y": 20, "z": 30}  # dict merged
    assert out["lst"] == [9]  # list replaced wholesale (overlay wins)


def test_apply_includes_merges_fragments_then_local_wins(tmp_path: Path):
    (tmp_path / "frag.yaml").write_text(
        "spec:\n  resources:\n    mcpServers: [{name: github}]\n  defaults: {harness: sdk}\n"
    )
    doc = {
        "include": ["frag.yaml"],
        "spec": {"defaults": {"harness": "cursor"}},  # local overrides fragment
    }
    merged = apply_includes(doc, tmp_path)
    assert "include" not in merged
    assert merged["spec"]["resources"]["mcpServers"] == [{"name": "github"}]
    assert merged["spec"]["defaults"]["harness"] == "cursor"  # local wins


def test_apply_includes_missing_fragment_errors(tmp_path: Path):
    with pytest.raises(LoadError):
        apply_includes({"include": ["nope.yaml"]}, tmp_path)


def test_interpolate_is_lazy_and_uses_resolver():
    doc = {
        "env_v": "${env:TOKEN}",
        "secret_v": "${secret:k8s-token}",
        "plain": "no placeholder",
        "nested": {"lst": ["${file:./x.txt}"]},
    }
    resolved = interpolate(doc, lambda kind, arg: f"<{kind}:{arg}>")
    assert resolved["env_v"] == "<env:TOKEN>"
    assert resolved["secret_v"] == "<secret:k8s-token>"
    assert resolved["plain"] == "no placeholder"
    assert resolved["nested"]["lst"] == ["<file:./x.txt>"]
    # The original document is untouched (interpolation never mutates in place).
    assert doc["env_v"] == "${env:TOKEN}"


def test_find_interpolations_lists_all_placeholders():
    doc = {"a": "${env:A}", "b": {"c": "${secret:B}", "d": "${file:C}"}}
    found = set(find_interpolations(doc))
    assert found == {("env", "A"), ("secret", "B"), ("file", "C")}
