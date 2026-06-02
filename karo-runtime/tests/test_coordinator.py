"""Coordinator tests: lifecycle, pipeline deps, dry-run, resume, guards."""

from pathlib import Path

import pytest

from karo_runtime.runtime import Coordinator
from karo_runtime.spec import compile_flat, compile_folder


def _flat(tmp_path: Path, body: str):
    f = tmp_path / "team.yaml"
    f.write_text("apiVersion: karo.dev/v1\nkind: AgentTeam\n" + body)
    return compile_flat(f).team


async def test_lead_crew_runs_to_completion(lead_crew: Path):
    team = compile_folder(lead_crew).team
    coord = Coordinator(team, project_dir=lead_crew, dry_run=True)
    res = await coord.run("objective")
    # one task per teammate (worker); planner is the lead
    assert all(t.owner == "worker" for t in res.tasks)


async def test_dry_run_plans_without_executing(lead_crew: Path):
    team = compile_folder(lead_crew).team
    coord = Coordinator(team, project_dir=lead_crew, dry_run=True)
    res = await coord.run("obj")
    assert all(t.state == "pending" for t in res.tasks)
    assert not res.completed


async def test_full_run_completes_and_records(tmp_path: Path):
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { model: { provider: anthropic, id: m }, permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk, instructions: go }]
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=False)
    res = await coord.run("do the thing")
    assert res.completed
    assert all(t.state == "done" for t in res.tasks)
    # memory recorded a result for each task
    recs = await coord.memory.query("team")
    assert len(recs) == len(res.tasks)


async def test_pipeline_creates_dependency_chain(tmp_path: Path):
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: pipeline, pipeline: { stages: [a, b, c] } }
  interaction: { autonomy: autonomous }
  agents:
    - { name: a, harness: sdk }
    - { name: b, harness: sdk }
    - { name: c, harness: sdk }
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=True)
    tasks = await coord.plan("obj")
    by_owner = {t.owner: t for t in tasks}
    assert by_owner["a"].depends_on == []
    assert by_owner["b"].depends_on == [by_owner["a"].id]
    assert by_owner["c"].depends_on == [by_owner["b"].id]


async def test_resume_keeps_existing_tasks(tmp_path: Path):
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk }]
""")
    c1 = Coordinator(team, project_dir=tmp_path, dry_run=True)
    first = await c1.plan("obj")
    c2 = Coordinator(team, project_dir=tmp_path, dry_run=True)
    again = await c2.plan("obj")
    assert {t.id for t in first} == {t.id for t in again}


async def test_supervised_pause_on_task_complete(tmp_path: Path):
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction:
    autonomy: supervised
    guards: [{ pauseOn: taskComplete }]
  agents: [{ name: a, harness: sdk }]
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=False)
    res = await coord.run("obj")
    assert "a" in res.paused_agents
    assert any(t.state == "paused" for t in res.tasks)


async def test_budget_hardstop_halts(tmp_path: Path):
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  budgets: { team: { provider: anthropic, limit: 1, window: session, onExceed: hardstop } }
  coordination: { pattern: swarm }
  interaction: { autonomy: autonomous }
  agents: [{ name: a, harness: sdk }]
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=False)
    res = await coord.run("a very long objective that exceeds one token easily")
    assert res.halted
    assert res.halt_reason == "budget-hardstop"
