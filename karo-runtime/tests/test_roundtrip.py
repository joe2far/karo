"""Folder-compile == flat-team.yaml round-trip parity (§4.0.1).

§4.0.1 claims a folder and the equivalent hand-written flat ``team.yaml`` produce
equivalent output *after canonicalization*. This guards the documented
equivalence and the frontmatter-body vs ``instructions: |`` normalization (the
plausible break: AGENT.md bodies are stripped while flat block scalars carry a
trailing newline).
"""

import textwrap
from pathlib import Path

from karo_runtime.spec import compile_flat, compile_folder, parity_projection


def test_folder_and_flat_are_canonically_equivalent(tmp_path: Path):
    # --- folder form ---
    folder = tmp_path / "folder"
    (folder / "agents" / "planner").mkdir(parents=True)
    (folder / "agents" / "worker").mkdir(parents=True)
    (folder / "karo.yaml").write_text(
        textwrap.dedent(
            """\
            apiVersion: karo.dev/v1
            kind: AgentTeam
            metadata:
              name: crew
            spec:
              defaults:
                harness: sdk
                model: { provider: anthropic, id: claude-opus-4-8 }
              coordination:
                pattern: lead-and-teammates
                lead: planner
              agents:
                - { ref: agents/planner }
                - { ref: agents/worker }
            """
        )
    )
    (folder / "agents" / "planner" / "AGENT.md").write_text(
        "---\nname: planner\nharness: sdk\n---\nYou are the lead.\n"
    )
    (folder / "agents" / "worker" / "AGENT.md").write_text(
        "---\nname: worker\nharness: sdk\n---\nYou implement.\n"
    )

    # --- equivalent flat form ---
    flat = tmp_path / "team.yaml"
    flat.write_text(
        textwrap.dedent(
            """\
            apiVersion: karo.dev/v1
            kind: AgentTeam
            metadata:
              name: crew
            spec:
              defaults:
                harness: sdk
                model: { provider: anthropic, id: claude-opus-4-8 }
              coordination:
                pattern: lead-and-teammates
                lead: planner
              agents:
                - name: planner
                  harness: sdk
                  instructions: You are the lead.
                - name: worker
                  harness: sdk
                  instructions: You implement.
            """
        )
    )

    folder_proj = parity_projection(compile_folder(folder).team)
    flat_proj = parity_projection(compile_flat(flat).team)
    assert folder_proj == flat_proj


def test_operator_projected_camelcase_doc_loads(tmp_path: Path):
    """The operator projects the team as a camelCase JSON doc (teamDocJSON in
    provisioner.go) that the agent pod loads via compile_flat. This guards the
    Go-json-tag ⇄ pydantic-alias compatibility: every camelCase field below must
    survive the round-trip, or the cluster pod would silently drop config."""
    import json

    doc = {
        "apiVersion": "karo.dev/v1",
        "kind": "AgentTeam",
        "metadata": {"name": "crew"},
        "spec": {
            "defaults": {"harness": "sdk", "permissionMode": "bypass",
                         "workingDir": "./ws", "model": {"provider": "anthropic", "id": "m"}},
            "budgets": {"team": {"provider": "anthropic", "limit": 5000000,
                                 "window": "daily", "onExceed": "pause"}, "perAgent": True},
            "resources": {"mcpServers": [{"name": "github", "transport": "stdio",
                                          "command": ["gh"]}],
                          "skills": [{"source": "./skills/k"}],
                          "tools": [{"name": "lk", "module": "t.py:lk"}]},
            "memory": {"backend": "file", "scope": "team",
                       "retention": {"maxItems": 10, "gcStrategy": "lru"}},
            "coordination": {"pattern": "pipeline",
                             "pipeline": {"stages": ["a", "b"]},
                             "mailbox": {"backend": "file", "hardLimit": 99},
                             "taskLayer": {"backend": "file", "resumable": True}},
            "interaction": {"attachable": True, "autonomy": "autonomous",
                            "pauseTimeout": 0},
            "agents": [
                {"name": "a", "harness": "sdk", "permissionMode": "bypass"},
                {"name": "b", "harness": "sdk", "budget": {"share": 0.5}},
            ],
        },
    }
    path = tmp_path / "team.json"
    path.write_text(json.dumps(doc), encoding="utf-8")
    team = compile_flat(path).team  # must not raise / drop fields

    assert team.spec.coordination.mailbox.hard_limit == 99
    assert team.spec.coordination.task_layer.resumable is True
    assert team.spec.resources.mcp_servers[0].name == "github"
    assert team.spec.memory.retention.max_items == 10
    assert team.spec.budgets.per_agent is True
    assert team.spec.agents[1].budget.share == 0.5
