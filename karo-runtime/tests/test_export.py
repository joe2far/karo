"""Export + round-trip parity (CLI §12, §19; v2 §15)."""

import textwrap
from pathlib import Path

import pytest

from karo_runtime.exporter import ExportError, export_manifest
from karo_runtime.spec import compile_flat, parity_projection
from karo_runtime.spec.models import AgentTeam


def _team(tmp_path: Path, body: str) -> AgentTeam:
    f = tmp_path / "team.yaml"
    f.write_text("apiVersion: karo.dev/v1\nkind: AgentTeam\n" + body)
    return compile_flat(f).team


def test_export_adds_runtime_and_namespace(tmp_path: Path):
    team = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk }]
""")
    doc, report = export_manifest(team, namespace="agents", profile="prod")
    assert doc["metadata"]["namespace"] == "agents"
    assert doc["runtime"]["idleTimeoutSeconds"] == 300
    assert doc["runtime"]["backends"]["tasks"] == {"kind": "postgres", "secretRef": {"name": "karo-pg"}}


def test_export_profiles_differ(tmp_path: Path):
    team = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk }]
""")
    staging, _ = export_manifest(team, profile="staging")
    prod, _ = export_manifest(team, profile="prod")
    assert staging["runtime"]["idleTimeoutSeconds"] == 120
    assert staging["runtime"]["maxConcurrentAgents"] == 5
    assert prod["runtime"]["maxConcurrentAgents"] == 10


def test_export_rejects_local_harness(tmp_path: Path):
    team = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: cursor }]
""")
    with pytest.raises(ExportError):
        export_manifest(team, profile="prod")
    # allow_local_harness downgrades to a warning
    doc, report = export_manifest(team, profile="prod", allow_local_harness=True)
    assert any("local-only" in w for w in report.warnings)


def test_export_coerces_prompt_permission_mode(tmp_path: Path):
    team = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: prompt }
  coordination: { pattern: swarm }
  agents: [{ name: a, harness: sdk }]
""")
    doc, report = export_manifest(team, profile="prod", coerce_prompt=True)
    assert doc["spec"]["agents"][0]["permissionMode"] == "plan"
    assert any("coerced" in t for t in report.transforms)


def test_export_collects_secret_refs_never_values(tmp_path: Path):
    team = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  resources:
    mcpServers:
      - { name: gh, transport: stdio, command: [gh], env: { TOKEN: "${secret:gh-token}" } }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk, mcp: [gh] }]
""")
    doc, report = export_manifest(team, profile="prod")
    assert "gh-token" in report.secrets
    assert "gh-token" in doc["runtime"]["secrets"]
    # the literal secret VALUE is never emitted; only the placeholder/ref form
    assert "secretRef" in str(doc["runtime"]["secrets"]["gh-token"])


def test_parity_spec_body_stable_across_export(tmp_path: Path):
    """The exported spec body equals the local spec after canonicalization
    (namespace + runtime excluded). Uses a large int + block scalar to catch
    YAML-portability drift (§19)."""
    body = """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass, model: { provider: anthropic, id: m } }
  budgets: { team: { provider: anthropic, limit: 5000000, window: daily } }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents:
    - name: a
      harness: sdk
      instructions: |
        line one
        line two
"""
    team = _team(tmp_path, body)
    local = parity_projection(team)
    doc, _ = export_manifest(team, namespace="agents", profile="prod", coerce_prompt=False)
    # Reconstruct an AgentTeam from the exported spec (drop runtime/namespace).
    exported = AgentTeam.model_validate({
        "apiVersion": doc["apiVersion"],
        "kind": doc["kind"],
        "metadata": {"name": doc["metadata"]["name"], "labels": doc["metadata"].get("labels", {})},
        "spec": doc["spec"],
    })
    assert parity_projection(exported) == local
    assert "5000000" in local and "5_000_000" not in local
