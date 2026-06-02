import textwrap
from pathlib import Path

import pytest


@pytest.fixture
def lead_crew(tmp_path: Path) -> Path:
    """A minimal valid lead-and-teammates folder."""
    (tmp_path / "agents" / "planner").mkdir(parents=True)
    (tmp_path / "agents" / "worker").mkdir(parents=True)
    (tmp_path / "karo.yaml").write_text(
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
              budgets:
                team: { provider: anthropic, limit: 5000000, window: daily, onExceed: pause }
              coordination:
                pattern: lead-and-teammates
                lead: planner
              agents:
                - { ref: agents/planner }
                - { ref: agents/worker }
            """
        )
    )
    (tmp_path / "agents" / "planner" / "AGENT.md").write_text(
        "---\nname: planner\nharness: sdk\n---\nYou are the lead.\n"
    )
    (tmp_path / "agents" / "worker" / "AGENT.md").write_text(
        "---\nname: worker\nharness: sdk\n---\nYou implement.\n"
    )
    return tmp_path
