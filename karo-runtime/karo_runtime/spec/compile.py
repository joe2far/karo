"""Compile the local folder convention (§4.0) into the compiled ``AgentTeam``.

This is the deterministic source → build-artifact step. The same code runs in
the CLI and the operator's agent image, so ``karo export`` and the operator
agree on the compiled form by construction (CLI §6.1, v2 §3.1).

Mapping (CLI §4.0.1):

    karo.yaml spec.{defaults,budgets,coordination,interaction}  -> verbatim
    agents/<n>/AGENT.md frontmatter                              -> spec.agents[]
    agents/<n>/AGENT.md body                                     -> instructions
    skills/<n>/                                                  -> resources.skills[]
    tools/*.py @tool fns                                         -> resources.tools[]
    mcp/servers.yaml | mcp/*.yaml                                -> resources.mcpServers[]
    shared/* via include:                                        -> deep-merged
"""

from __future__ import annotations

import ast
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


from . import frontmatter
from .loader import LoadError, apply_includes, load_yaml
from .models import AgentTeam


@dataclass
class CompileResult:
    team: AgentTeam
    raw: dict[str, Any]
    # source_map: agent name / resource -> on-disk file, for diagnostics (§7)
    source_map: dict[str, str] = field(default_factory=dict)


def _discover_tool_functions(py_file: Path) -> list[str]:
    """Return names of ``@tool``-decorated functions in ``py_file`` via AST.

    AST-only (never imports/executes the module) so ``karo compile`` is safe on
    untrusted folders.
    """
    names: list[str] = []
    try:
        tree = ast.parse(py_file.read_text(encoding="utf-8"))
    except SyntaxError:
        return names
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            for dec in node.decorator_list:
                target = dec.func if isinstance(dec, ast.Call) else dec
                name = getattr(target, "id", None) or getattr(target, "attr", None)
                if name == "tool":
                    names.append(node.name)
                    break
    return names


def _discover_resources(root: Path, raw_spec: dict[str, Any]) -> dict[str, Any]:
    """Merge auto-discovered skills/tools/mcp under raw_spec['resources']."""
    resources: dict[str, Any] = dict(raw_spec.get("resources") or {})

    # skills/<name>/SKILL.md
    skills_dir = root / "skills"
    discovered_skills: list[dict[str, str]] = []
    if skills_dir.is_dir():
        for child in sorted(p for p in skills_dir.iterdir() if p.is_dir()):
            if (child / "SKILL.md").is_file():
                discovered_skills.append({"source": f"./skills/{child.name}"})
    existing_skill_sources = {s.get("source") for s in resources.get("skills", [])}
    resources["skills"] = list(resources.get("skills", [])) + [
        s for s in discovered_skills if s["source"] not in existing_skill_sources
    ]

    # tools/*.py @tool functions
    tools_dir = root / "tools"
    discovered_tools: list[dict[str, Any]] = []
    if tools_dir.is_dir():
        for py in sorted(tools_dir.glob("*.py")):
            for fn in _discover_tool_functions(py):
                discovered_tools.append(
                    {"name": fn, "module": f"./tools/{py.name}:{fn}"}
                )
    existing_tool_names = {t.get("name") for t in resources.get("tools", [])}
    resources["tools"] = list(resources.get("tools", [])) + [
        t for t in discovered_tools if t["name"] not in existing_tool_names
    ]

    # mcp/servers.yaml or mcp/*.yaml
    mcp_dir = root / "mcp"
    discovered_mcp: list[dict[str, Any]] = []
    if mcp_dir.is_dir():
        for yml in sorted(mcp_dir.glob("*.yaml")):
            data = load_yaml(yml)
            servers = data.get("servers") if isinstance(data, dict) else None
            if isinstance(servers, list):
                discovered_mcp.extend(servers)
    existing_mcp_names = {m.get("name") for m in resources.get("mcpServers", [])}
    resources["mcpServers"] = list(resources.get("mcpServers", [])) + [
        m for m in discovered_mcp if m.get("name") not in existing_mcp_names
    ]

    # Prune empty keys so canonical output is clean.
    return {k: v for k, v in resources.items() if v}


def compile_folder(root: Path) -> CompileResult:
    """Compile a §4.0 folder rooted at ``root`` into an ``AgentTeam``."""
    root = Path(root)
    karo_yaml = root / "karo.yaml"
    if not karo_yaml.is_file():
        raise LoadError(
            "no karo.yaml found; is this a karo project folder?", file=str(karo_yaml)
        )

    doc = load_yaml(karo_yaml)
    doc = apply_includes(doc, root)
    raw_spec: dict[str, Any] = dict(doc.get("spec") or {})

    source_map: dict[str, str] = {}

    # Discover / order agents.
    agents_dir = root / "agents"
    discovered: dict[str, Path] = {}
    if agents_dir.is_dir():
        for child in sorted(p for p in agents_dir.iterdir() if p.is_dir()):
            if (child / "AGENT.md").is_file():
                discovered[child.name] = child / "AGENT.md"

    # The agents: list in karo.yaml is optional ordering/inclusion control.
    declared = raw_spec.get("agents")
    ordered_names: list[str]
    if declared:
        ordered_names = []
        for entry in declared:
            ref = entry.get("ref") if isinstance(entry, dict) else None
            if ref:
                ordered_names.append(Path(ref).name)
            elif isinstance(entry, dict) and "name" in entry:
                # Inline agent authored directly in karo.yaml (flat-ish).
                ordered_names.append(entry["name"])
    else:
        ordered_names = list(discovered.keys())

    compiled_agents: list[dict[str, Any]] = []
    inline_by_name = {
        e["name"]: e
        for e in (declared or [])
        if isinstance(e, dict) and "name" in e and "ref" not in e
    }
    for name in ordered_names:
        if name in discovered:
            md = frontmatter.parse(discovered[name].read_text(encoding="utf-8"))
            agent: dict[str, Any] = dict(md.data)
            agent.setdefault("name", name)
            agent["instructions"] = md.body
            source_map[f"agent:{name}"] = str(
                discovered[name].relative_to(root)
            )
            compiled_agents.append(agent)
        elif name in inline_by_name:
            compiled_agents.append(dict(inline_by_name[name]))
            source_map[f"agent:{name}"] = "karo.yaml"
        else:
            raise LoadError(
                f"agent {name!r} referenced in karo.yaml but no "
                f"agents/{name}/AGENT.md found",
                file="karo.yaml",
            )

    raw_spec["agents"] = compiled_agents
    raw_spec["resources"] = _discover_resources(root, raw_spec)

    raw_doc: dict[str, Any] = {
        "apiVersion": doc.get("apiVersion", "karo.dev/v1"),
        "kind": doc.get("kind", "AgentTeam"),
        "metadata": doc.get("metadata", {}),
        "spec": raw_spec,
    }

    team = AgentTeam.model_validate(raw_doc)
    return CompileResult(team=team, raw=raw_doc, source_map=source_map)


def compile_flat(path: Path) -> CompileResult:
    """Load a pre-compiled / flat single-file ``team.yaml`` (CLI §4.1)."""
    path = Path(path)
    doc = load_yaml(path)
    doc = apply_includes(doc, path.parent)
    team = AgentTeam.model_validate(doc)
    return CompileResult(team=team, raw=doc, source_map={"flat": str(path.name)})


def compile_path(path: Path) -> CompileResult:
    """Dispatch: a directory compiles as a folder; a file as flat team.yaml."""
    path = Path(path)
    if path.is_dir():
        return compile_folder(path)
    return compile_flat(path)
