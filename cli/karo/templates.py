"""Scaffold templates for ``karo init`` (CLI §7).

Each template IS a §4.0 folder skeleton. Scaffolded templates contain **no**
org-specific identifiers (CI-checked, CLI §21).
"""

from __future__ import annotations

from pathlib import Path

GITIGNORE = """# karo local runtime state
.karo/
# user secrets / config never committed
*.local.yaml
"""

PROJECT_README = """# {name}

A KARO agent team. Author agents under `agents/<role>/AGENT.md`, shared skills
under `skills/`, custom tools under `tools/`, and MCP servers in
`mcp/servers.yaml`. The thin `karo.yaml` ties them together.

```bash
karo validate            # static checks (no network)
karo run -o "your objective here"
karo export -o team-manifest.yaml --namespace agents   # hand off to KARO v2
```
"""

MCP_SERVERS = """# MCP servers shared across the team (CLI §9).
# servers:
#   - name: github
#     transport: stdio
#     command: ["gh-mcp-server"]
#     env:
#       GITHUB_TOKEN: ${env:GITHUB_TOKEN}
servers: []
"""

EXAMPLE_TOOL = '''"""Example custom in-process tool (CLI §9).

Functions decorated with @tool are auto-discovered and registered by function
name. Replace this with your own.
"""


def tool(fn):  # minimal stand-in decorator for the @tool marker
    fn.__is_karo_tool__ = True
    return fn


@tool
def lookup(key: str) -> str:
    """Fetch a record by key."""
    return f"looked up {key}"
'''


def _karo_yaml(name: str, pattern: str, lead: str | None, stages: list[str] | None, agents: list[str]) -> str:
    lines = [
        "apiVersion: karo.dev/v1",
        "kind: AgentTeam",
        "metadata:",
        f"  name: {name}",
        "  labels: { domain: platform }",
        "spec:",
        "  defaults:",
        "    harness: sdk",
        "    model: { provider: anthropic, id: claude-opus-4-8 }",
        "  budgets:",
        "    team: { provider: anthropic, limit: 5000000, window: daily, onExceed: pause }",
        "  coordination:",
    ]
    if pattern == "pipeline":
        lines.append("    pattern: pipeline")
        lines.append(f"    pipeline: {{ stages: [{', '.join(stages or [])}] }}")
    elif pattern == "swarm":
        lines.append("    pattern: swarm")
    else:
        lines.append("    pattern: lead-and-teammates")
        lines.append(f"    lead: {lead}")
    lines += [
        "  interaction: { attachable: true, autonomy: supervised }",
        "  agents:",
    ]
    for a in agents:
        lines.append(f"    - {{ ref: agents/{a} }}")
    return "\n".join(lines) + "\n"


def _agent_md(name: str, harness: str, model_id: str, body: str, extra: str = "") -> str:
    fm = [
        "---",
        f"name: {name}",
        f"harness: {harness}",
        f"model: {{ provider: anthropic, id: {model_id} }}",
    ]
    if extra:
        fm.append(extra)
    fm.append("---")
    return "\n".join(fm) + "\n" + body.strip() + "\n"


# role bodies kept generic / org-neutral
_PLANNER = """You are the lead. Decompose the incoming objective into tasks with explicit
acceptance criteria, assign them to teammates, and coordinate via the shared task
list. Do not write code yourself.
"""
_IMPLEMENTER = """You implement assigned tasks to their acceptance criteria. You run code, edit
files, and report results back to the planner's mailbox.
"""
_REVIEWER = """You review completed work against acceptance criteria. You never edit; you return
tasks with specific, actionable feedback.
"""


def template_files(template: str, name: str) -> dict[str, str]:
    """Return a mapping of relative path -> file contents for a template."""
    common = {
        ".gitignore": GITIGNORE,
        "README.md": PROJECT_README.format(name=name),
        "mcp/servers.yaml": MCP_SERVERS,
        "tools/example.py": EXAMPLE_TOOL,
        "skills/.gitkeep": "",
        "shared/.gitkeep": "",
    }

    if template == "minimal":
        agents = ["assistant"]
        files = {
            "karo.yaml": _karo_yaml(name, "lead-and-teammates", "assistant", None, agents),
            "agents/assistant/AGENT.md": _agent_md(
                "assistant", "sdk", "claude-opus-4-8",
                "You are a helpful single agent. Complete the objective end to end.",
            ),
        }
    elif template == "pipeline":
        agents = ["planner", "implementer", "reviewer"]
        files = {
            "karo.yaml": _karo_yaml(name, "pipeline", None, agents, agents),
            "agents/planner/AGENT.md": _agent_md("planner", "sdk", "claude-opus-4-8", _PLANNER),
            "agents/implementer/AGENT.md": _agent_md("implementer", "sdk", "claude-sonnet-4-6", _IMPLEMENTER),
            "agents/reviewer/AGENT.md": _agent_md("reviewer", "sdk", "claude-sonnet-4-6", _REVIEWER,
                                                  extra="interaction: { autonomy: autonomous }\npermissionMode: bypass"),
        }
    else:  # lead-team (default)
        agents = ["planner", "implementer", "reviewer"]
        files = {
            "karo.yaml": _karo_yaml(name, "lead-and-teammates", "planner", None, agents),
            "agents/planner/AGENT.md": _agent_md("planner", "sdk", "claude-opus-4-8", _PLANNER),
            "agents/implementer/AGENT.md": _agent_md("implementer", "sdk", "claude-sonnet-4-6", _IMPLEMENTER),
            "agents/reviewer/AGENT.md": _agent_md("reviewer", "sdk", "claude-sonnet-4-6", _REVIEWER,
                                                  extra="interaction: { autonomy: autonomous }\npermissionMode: bypass"),
        }

    files.update(common)
    return files


def flat_team_yaml(name: str) -> str:
    """A single inline ``team.yaml`` for ``karo init --flat`` (CLI §4.1)."""
    return f"""apiVersion: karo.dev/v1
kind: AgentTeam
metadata:
  name: {name}
  labels: {{ domain: platform }}
spec:
  defaults:
    harness: sdk
    model: {{ provider: anthropic, id: claude-opus-4-8 }}
  budgets:
    team: {{ provider: anthropic, limit: 5000000, window: daily, onExceed: pause }}
  coordination:
    pattern: lead-and-teammates
    lead: planner
  interaction: {{ attachable: true, autonomy: supervised }}
  agents:
    - name: planner
      instructions: |
        You are the lead. Decompose the objective into tasks with acceptance
        criteria, assign them, and coordinate. Do not write code yourself.
      harness: sdk
      model: {{ provider: anthropic, id: claude-opus-4-8 }}
    - name: implementer
      instructions: |
        You implement assigned tasks to their acceptance criteria and report back.
      harness: sdk
      model: {{ provider: anthropic, id: claude-sonnet-4-6 }}
    - name: reviewer
      instructions: |
        You review completed work against acceptance criteria and return feedback.
      harness: sdk
      model: {{ provider: anthropic, id: claude-sonnet-4-6 }}
      interaction: {{ autonomy: autonomous }}
      permissionMode: bypass
"""


def write_files(root: Path, files: dict[str, str]) -> list[Path]:
    written: list[Path] = []
    for rel, content in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        written.append(path)
    return written
