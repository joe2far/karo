"""Cross-field validation rules (CLI §4.3)."""

from pathlib import Path

from karo_runtime.spec import compile_flat, validate


def _team(tmp_path: Path, body: str):
    f = tmp_path / "team.yaml"
    f.write_text("apiVersion: karo.dev/v1\nkind: AgentTeam\n" + body)
    return compile_flat(f)


def codes(diags):
    return {d.code for d in diags if d.severity == "error"}


def test_lead_required_for_lead_pattern(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: lead-and-teammates }
  agents: [{ name: a, harness: sdk }]
""")
    assert "lead-required" in codes(validate(res))


def test_lead_unexpected_for_swarm(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: swarm, lead: a }
  agents: [{ name: a, harness: sdk }]
""")
    assert "lead-unexpected" in codes(validate(res))


def test_pipeline_requires_stages_and_resolves(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: pipeline, pipeline: { stages: [a, ghost] } }
  agents: [{ name: a, harness: sdk }]
""")
    c = codes(validate(res))
    assert "pipeline-unknown" in c


def test_pipeline_cycle_on_repeat(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: pipeline, pipeline: { stages: [a, a] } }
  agents: [{ name: a, harness: sdk }]
""")
    assert "pipeline-cycle" in codes(validate(res))


def test_unknown_resource_refs(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: swarm }
  agents: [{ name: a, harness: sdk, tools: [nope], mcp: [missing], skills: [gone] }]
""")
    c = codes(validate(res))
    assert {"unknown-tool", "unknown-mcp", "unknown-skill"} <= c


def test_unknown_repo_ref(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: swarm }
  agents: [{ name: a, harness: sdk, repos: [ghost] }]
""")
    assert "unknown-repo" in codes(validate(res))


def test_known_repo_ref_ok(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: swarm }
  resources: { repos: [{ name: app, url: "https://example.com/app.git" }] }
  agents: [{ name: a, harness: sdk, repos: [app] }]
""")
    assert "unknown-repo" not in codes(validate(res))


def test_unknown_reviewer(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: lead-and-teammates, lead: planner, reviewer: ghost }
  agents: [{ name: planner, harness: sdk }, { name: worker, harness: sdk }]
""")
    assert "reviewer-unknown" in codes(validate(res))


def test_guard_matcher_resolution(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  coordination: { pattern: swarm }
  interaction:
    guards:
      - { pauseBefore: [Bash] }
      - { pauseBefore: [doesnotexist] }
  agents: [{ name: a, harness: sdk }]
""")
    c = codes(validate(res))
    assert "guard-matcher" in c  # only the unknown one fails


def test_builtin_and_mcp_guard_ok(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  resources: { mcpServers: [{ name: github, transport: stdio, command: [gh] }] }
  coordination: { pattern: swarm }
  interaction:
    guards:
      - { pauseBefore: ["Write*"] }
      - { pauseBefore: ["mcp:github/create_issue"] }
  agents: [{ name: a, harness: sdk, mcp: [github] }]
""")
    assert "guard-matcher" not in codes(validate(res))


def test_budget_overcommit(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  budgets: { team: { provider: anthropic, limit: 100 } }
  coordination: { pattern: swarm }
  agents:
    - { name: a, harness: sdk, budget: { limit: 80 } }
    - { name: b, harness: sdk, budget: { limit: 80 } }
""")
    assert "budget-overcommit" in codes(validate(res))


def test_prompt_autonomous_invalid(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: prompt }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk }]
""")
    assert "prompt-autonomous" in codes(validate(res))


def test_cluster_rejects_local_harness(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  agents: [{ name: a, harness: cursor }]
""")
    assert "harness-not-cluster" in codes(validate(res, target="cluster"))
    # local target allows it
    assert "harness-not-cluster" not in codes(validate(res, target="local"))


def test_metadata_name_dns1123(tmp_path):
    res = _team(tmp_path, """\
metadata: { name: Bad_Name }
spec:
  coordination: { pattern: swarm }
  agents: [{ name: a, harness: sdk }]
""")
    assert "metadata-name" in codes(validate(res))
