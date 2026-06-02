"""End-to-end CLI tests via Typer's CliRunner."""

import json
from pathlib import Path

from typer.testing import CliRunner

from karo.cli import app

runner = CliRunner()


def _init(tmp_path: Path, template: str = "lead-team", name: str = "demo") -> None:
    res = runner.invoke(app, ["-p", str(tmp_path), "init", "--name", name, "--template", template])
    assert res.exit_code == 0, res.output


def test_version_json():
    res = runner.invoke(app, ["--json", "version"])
    assert res.exit_code == 0
    data = json.loads(res.output)
    assert data["spec_apiVersion"] == "karo.dev/v1"


def test_init_validate_lead_crew(tmp_path: Path):
    _init(tmp_path)
    assert (tmp_path / "karo.yaml").exists()
    assert (tmp_path / "agents" / "planner" / "AGENT.md").exists()
    res = runner.invoke(app, ["-p", str(tmp_path), "validate"])
    assert res.exit_code == 0, res.output
    assert "ok" in res.output


def test_compile_yaml_and_json(tmp_path: Path):
    """`karo compile` must work in both formats (F5: yaml mode crashed on enums)."""
    _init(tmp_path)
    res = runner.invoke(app, ["-p", str(tmp_path), "compile"])  # default yaml
    assert res.exit_code == 0, res.output
    assert "apiVersion: karo.dev/v1" in res.output
    assert "kind: AgentTeam" in res.output
    res = runner.invoke(app, ["-p", str(tmp_path), "compile", "--format", "json"])
    assert res.exit_code == 0, res.output
    assert '"apiVersion":"karo.dev/v1"' in res.output


def test_attach_inject_message(tmp_path: Path):
    """`karo attach -m` injects a user turn and prints the agent reply (§13)."""
    _init(tmp_path)
    res = runner.invoke(app, ["-p", str(tmp_path), "attach", "planner", "-m", "do the API first"])
    assert res.exit_code == 0, res.output
    assert "planner" in res.output


def test_init_templates_have_no_org_identifiers(tmp_path: Path):
    """Templates must ship without org-specific identifiers (CLI §21, CI-checked)."""
    forbidden = ["joe2far", "joe2farrell", "@gmail", "anthropic.com/internal"]
    for template in ("minimal", "lead-team", "pipeline"):
        d = tmp_path / template
        d.mkdir()
        runner.invoke(app, ["-p", str(d), "init", "--name", "t", "--template", template])
        for f in d.rglob("*"):
            if f.is_file():
                text = f.read_text("utf-8").lower()
                for token in forbidden:
                    assert token not in text, f"{token} found in {f}"


def test_validate_cluster_reports_warnings(tmp_path: Path):
    _init(tmp_path)
    res = runner.invoke(app, ["-p", str(tmp_path), "validate", "--target", "cluster"])
    # prompt agents warn (not error) on cluster
    assert res.exit_code == 0
    assert "prompt-cluster" in res.output


def test_run_dry_run_and_state(tmp_path: Path):
    _init(tmp_path)
    res = runner.invoke(app, ["-p", str(tmp_path), "--json", "run", "-o", "obj", "--dry-run"])
    assert res.exit_code == 0, res.output
    data = json.loads(res.output)
    assert data["tasks"]
    assert all(t["state"] == "pending" for t in data["tasks"])


def test_full_run_then_inspect(tmp_path: Path):
    _init(tmp_path)
    res = runner.invoke(app, ["-p", str(tmp_path), "run", "-o", "ship it"])
    assert res.exit_code == 0, res.output
    assert "completed" in res.output
    res = runner.invoke(app, ["-p", str(tmp_path), "--json", "tasks", "list"])
    tasks = json.loads(res.output)
    assert tasks and all(t["state"] == "done" for t in tasks)


def test_export_yaml(tmp_path: Path):
    _init(tmp_path)
    out = tmp_path / "manifest.yaml"
    res = runner.invoke(app, ["-p", str(tmp_path), "export", "-o", str(out), "--namespace", "agents"])
    assert res.exit_code == 0, res.output
    text = out.read_text()
    assert "runtime:" in text
    assert "namespace: agents" in text


def test_schema_emits_json():
    res = runner.invoke(app, ["schema"])
    assert res.exit_code == 0
    assert json.loads(res.output)["title"] == "AgentTeam"


def test_mail_send_and_list(tmp_path: Path):
    _init(tmp_path)
    runner.invoke(app, ["-p", str(tmp_path), "run", "-o", "x", "--dry-run"])
    res = runner.invoke(app, ["-p", str(tmp_path), "mail", "send", "planner", "--body", "do X"])
    assert res.exit_code == 0
    res = runner.invoke(app, ["-p", str(tmp_path), "--json", "mail", "list", "planner"])
    msgs = json.loads(res.output)
    assert msgs[0]["body"] == "do X"
