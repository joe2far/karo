"""Spec tests: models, frontmatter, compile, canonicalize, validate, schema."""

import textwrap
from pathlib import Path

import pytest

from karo_runtime.spec import (
    compile_folder,
    json_schema,
    materialized_spec,
    parity_projection,
    validate,
)
from karo_runtime.spec import frontmatter
from karo_runtime.spec.compile import compile_flat
from karo_runtime.spec.models import AgentTeam
from karo_runtime.spec.loader import lint_numeric_literals


def test_frontmatter_splits_header_and_body():
    fm = frontmatter.parse("---\nname: a\nharness: sdk\n---\nHello body\nmore\n")
    assert fm.data == {"name": "a", "harness": "sdk"}
    assert fm.body == "Hello body\nmore"


def test_frontmatter_no_header_is_all_body():
    fm = frontmatter.parse("just body\n")
    assert fm.data == {}
    assert fm.body == "just body"


def test_frontmatter_unterminated_raises():
    with pytest.raises(ValueError):
        frontmatter.parse("---\nname: a\nbody without close")


def test_apiversion_rejected():
    with pytest.raises(Exception) as ei:
        AgentTeam.model_validate(
            {"apiVersion": "karo.dev/v2", "kind": "AgentTeam",
             "metadata": {"name": "x"}, "spec": {"agents": [{"name": "a"}]}}
        )
    assert "apiVersion" in str(ei.value)


def test_compile_autodiscovers_tools_skills_mcp(tmp_path: Path):
    (tmp_path / "agents" / "a").mkdir(parents=True)
    (tmp_path / "agents" / "a" / "AGENT.md").write_text("---\nname: a\n---\nbody\n")
    (tmp_path / "skills" / "kubectl").mkdir(parents=True)
    (tmp_path / "skills" / "kubectl" / "SKILL.md").write_text("# kubectl\n")
    (tmp_path / "tools").mkdir()
    (tmp_path / "tools" / "j.py").write_text(
        "def tool(f):\n    return f\n\n@tool\ndef lookup(key):\n    return key\n"
    )
    (tmp_path / "mcp").mkdir()
    (tmp_path / "mcp" / "servers.yaml").write_text(
        "servers:\n  - name: github\n    transport: stdio\n    command: [gh]\n"
    )
    (tmp_path / "karo.yaml").write_text(
        "apiVersion: karo.dev/v1\nkind: AgentTeam\nmetadata: { name: t }\nspec: {}\n"
    )
    res = compile_folder(tmp_path)
    spec = res.team.spec
    assert [s.source for s in spec.resources.skills] == ["./skills/kubectl"]
    assert [t.name for t in spec.resources.tools] == ["lookup"]
    assert [m.name for m in spec.resources.mcp_servers] == ["github"]
    assert spec.agents[0].instructions == "body"


def test_compile_and_validate_clean(lead_crew: Path):
    res = compile_folder(lead_crew)
    diags = validate(res)
    assert [d for d in diags if d.severity == "error"] == []


def test_canonical_materializes_defaults(lead_crew: Path):
    res = compile_folder(lead_crew)
    spec = materialized_spec(res.team)
    for agent in spec["agents"]:
        # defaults materialized onto every agent
        assert agent["harness"] == "sdk"
        assert agent["model"]["id"] == "claude-opus-4-8"
        assert agent["permissionMode"] == "prompt"
        assert "interaction" in agent
        assert agent["mailbox"] == agent["name"]


def test_parity_projection_excludes_namespace_and_runtime(lead_crew: Path):
    res = compile_folder(lead_crew)
    res.team.metadata.namespace = "agents"
    res.team.runtime = {"scaleToZero": True}
    proj = parity_projection(res.team)
    assert "namespace" not in proj
    assert "scaleToZero" not in proj
    assert proj.endswith("\n")


def test_numeric_underscore_lint():
    diags = lint_numeric_literals("spec:\n  limit: 5_000_000\n", "karo.yaml")
    assert len(diags) == 1
    assert diags[0]["code"] == "numeric-underscore"
    assert diags[0]["line"] == 2
    # plain decimal is fine
    assert lint_numeric_literals("limit: 5000000\n", "k") == []


def test_json_schema_has_agentteam_shape():
    schema = json_schema()
    assert schema["title"] == "AgentTeam"
    assert "properties" in schema
    assert "spec" in schema["properties"]


def test_flat_compile(tmp_path: Path):
    f = tmp_path / "team.yaml"
    f.write_text(
        textwrap.dedent(
            """\
            apiVersion: karo.dev/v1
            kind: AgentTeam
            metadata: { name: flat }
            spec:
              coordination: { pattern: swarm }
              agents:
                - { name: a, instructions: do it, harness: sdk }
            """
        )
    )
    res = compile_flat(f)
    assert res.team.metadata.name == "flat"
    assert res.team.spec.agents[0].name == "a"
