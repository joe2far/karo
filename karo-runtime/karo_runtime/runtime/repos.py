"""Git working repos for agents (§9).

A team declares the repos its agents work on under ``resources.repos``; agents
reference them by name. Before a local run the CLI clones/checks them out into
the workspace and points each agent's working directory at its repo. On cluster
the agent pod's init-container does the same from the same spec — so "which repo
this agent works on" is part of the portable definition, not per-machine setup.

Auth is intentionally **not** modeled here: cloning uses the caller's own git
configuration (ssh keys, credential helper, or a token embedded in the URL via
``${env:...}``). That keeps the shared team definition free of anyone's
credentials — a colleague clones the same team and authenticates as themselves.
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Callable, Optional

from ..spec.models import AgentTeam, Repo


class RepoError(RuntimeError):
    pass


def repo_path(repo: Repo, team: AgentTeam, project_dir: str | Path) -> Path:
    """Resolve a repo's checkout path (absolute) — ``path`` or ``<workingDir>/<name>``."""
    working_dir = (team.spec.defaults.working_dir or "./workspace").rstrip("/")
    rel = repo.path or f"{working_dir}/{repo.name}"
    p = Path(rel)
    if p.is_absolute():
        return p
    rel = rel[2:] if rel.startswith("./") else rel
    return Path(project_dir) / rel


def _git(args: list[str], cwd: Optional[Path] = None) -> str:
    proc = subprocess.run(["git", *args], cwd=cwd, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RepoError(f"git {' '.join(args)} failed: {(proc.stderr or proc.stdout).strip()}")
    return proc.stdout


def ensure_repos(
    team: AgentTeam,
    project_dir: str | Path,
    *,
    only_agents: Optional[set[str]] = None,
    on_event: Optional[Callable[[str, str, Path], None]] = None,
) -> dict[str, Path]:
    """Clone or update every declared repo (or just those used by ``only_agents``).

    Idempotent: clones a missing repo, otherwise fetches; checks out ``ref`` when
    set (branch, tag, or commit SHA) and fast-forwards a branch. Returns a map of
    repo name → checkout path. Local-only (the operator handles cluster cloning).
    """
    repos = list(team.spec.resources.repos)
    if only_agents is not None:
        wanted: set[str] = set()
        for a in team.spec.agents:
            if a.name in only_agents:
                wanted.update(a.repos)
        repos = [r for r in repos if r.name in wanted]

    out: dict[str, Path] = {}
    for repo in repos:
        dest = repo_path(repo, team, project_dir)
        out[repo.name] = dest
        if (dest / ".git").is_dir():
            _git(["fetch", "--all", "--tags", "--prune"], cwd=dest)
            action = "updated"
        else:
            dest.parent.mkdir(parents=True, exist_ok=True)
            _git(["clone", repo.url, str(dest)])
            action = "cloned"
        if repo.ref:
            _git(["checkout", repo.ref], cwd=dest)
            # Fast-forward if ref is a branch; ignore failure (tag/SHA are detached).
            subprocess.run(["git", "-C", str(dest), "pull", "--ff-only"],
                           capture_output=True, text=True)
        if on_event:
            on_event(repo.name, action, dest)
    return out
