"""Git working-repo materialization (§9): clone/update + per-agent working dir."""

import subprocess
from pathlib import Path

import pytest

from karo_runtime.runtime import Coordinator
from karo_runtime.runtime.repos import ensure_repos, repo_path
from karo_runtime.spec import compile_flat


def _git(args, cwd):
    subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True, text=True)


def _source_repo(tmp_path: Path) -> Path:
    """Create a local git repo to act as a clone source (offline)."""
    src = tmp_path / "source.git"
    src.mkdir()
    _git(["init", "-q"], src)
    _git(["config", "user.email", "t@t"], src)
    _git(["config", "user.name", "t"], src)
    _git(["config", "commit.gpgsign", "false"], src)
    (src / "README.md").write_text("hello\n")
    _git(["add", "."], src)
    _git(["-c", "commit.gpgsign=false", "commit", "-qm", "init"], src)
    return src


def _team(tmp_path: Path, url: str):
    f = tmp_path / "team.yaml"
    f.write_text(f"""\
apiVersion: karo.dev/v1
kind: AgentTeam
metadata: {{ name: t }}
spec:
  defaults: {{ permissionMode: bypass, workingDir: ./workspace }}
  coordination: {{ pattern: swarm }}
  interaction: {{ autonomy: autonomous }}
  resources:
    repos:
      - {{ name: app, url: "{url}" }}
  agents:
    - {{ name: dev, harness: sdk, repos: [app] }}
""")
    return compile_flat(f).team


def test_ensure_repos_clones_then_updates(tmp_path: Path):
    src = _source_repo(tmp_path)
    proj = tmp_path / "proj"
    proj.mkdir()
    team = _team(tmp_path, str(src))

    paths = ensure_repos(team, proj)
    dest = paths["app"]
    assert dest == proj / "workspace" / "app"
    assert (dest / ".git").is_dir()
    assert (dest / "README.md").read_text() == "hello\n"

    # Idempotent: a second call updates in place without error.
    again = ensure_repos(team, proj)
    assert again["app"] == dest


def test_only_agents_filters_repos(tmp_path: Path):
    src = _source_repo(tmp_path)
    proj = tmp_path / "proj"
    proj.mkdir()
    team = _team(tmp_path, str(src))
    # No agent named 'other' uses repos → nothing cloned.
    assert ensure_repos(team, proj, only_agents={"other"}) == {}
    assert not (proj / "workspace" / "app").exists()


def test_agent_working_dir_is_its_repo(tmp_path: Path):
    src = _source_repo(tmp_path)
    proj = tmp_path / "proj"
    proj.mkdir()
    team = _team(tmp_path, str(src))
    coord = Coordinator(team, project_dir=proj, dry_run=True)
    ctx = coord._context("dev")
    assert ctx.working_dir == str(repo_path(team.spec.resources.repos[0], team, proj))
    assert ctx.working_dir.endswith("workspace/app")


def test_missing_repo_url_raises(tmp_path: Path):
    proj = tmp_path / "proj"
    proj.mkdir()
    team = _team(tmp_path, str(tmp_path / "does-not-exist"))
    with pytest.raises(Exception):
        ensure_repos(team, proj)
