"""Coordinator tests: lifecycle, pipeline deps, dry-run, resume, guards."""

from pathlib import Path


from karo_runtime.runtime import Coordinator
from karo_runtime.spec import compile_flat, compile_folder


def _flat(tmp_path: Path, body: str):
    f = tmp_path / "team.yaml"
    f.write_text("apiVersion: karo.dev/v1\nkind: AgentTeam\n" + body)
    return compile_flat(f).team


async def test_lead_crew_decomposes_and_synthesizes(lead_crew: Path):
    """M2 lead-and-teammates: one teammate task + a lead synthesis task gated on it."""
    team = compile_folder(lead_crew).team
    coord = Coordinator(team, project_dir=lead_crew, dry_run=True)
    res = await coord.run("objective")
    by_owner = {t.owner: t for t in res.tasks}
    assert "worker" in by_owner and "planner" in by_owner  # teammate + lead synth
    synth = by_owner["planner"]
    assert synth.depends_on == [by_owner["worker"].id]  # synthesis waits on teammate


async def test_lead_crew_runs_to_completion_with_handoff(lead_crew: Path):
    """Full run: teammate works, reports to the lead's mailbox, lead synthesizes."""
    team = compile_folder(lead_crew).team
    coord = Coordinator(team, project_dir=lead_crew, dry_run=False)
    res = await coord.run("ship the feature")
    assert res.completed
    assert all(t.state == "done" for t in res.tasks)
    # the worker reported to the lead's (planner) mailbox (mailbox handoff)
    inbox = await coord.mail.inbox("planner")
    assert any(m.sender == "worker" for m in inbox)


async def test_lead_and_teammates_review_state(tmp_path: Path):
    """A reviewer agent drives the `review` state before a task is done (M2)."""
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: lead-and-teammates, lead: planner }
  interaction: { autonomy: autonomous }
  agents:
    - { name: planner, harness: sdk }
    - { name: implementer, harness: sdk }
    - { name: reviewer, harness: sdk }
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=False)
    res = await coord.run("build it")
    assert res.completed
    # the implementer task carried a reviewer and the reviewer produced memory
    impl = next(t for t in res.tasks if t.owner == "implementer")
    assert impl.reviewer == "reviewer"
    recs = await coord.memory.query("team")
    assert any("reviewer" in r.tags for r in recs)


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


async def test_pause_before_guard_pauses_then_continues(tmp_path: Path):
    """pauseBefore pauses a supervised agent before acting; --continue releases it."""
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction:
    autonomy: supervised
    guards: [{ pauseBefore: [Bash] }]
  agents: [{ name: a, harness: sdk }]
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=False)
    res = await coord.run("obj")
    assert "a" in res.paused_agents
    assert any(t.state == "paused" for t in res.tasks)

    # Attach + continue releases the guard; a resumed run completes.
    released = await coord.release_paused("a")
    assert released
    coord2 = Coordinator(team, project_dir=tmp_path, dry_run=False, run_id=coord.run_id)
    res2 = await coord2.run("obj")
    assert res2.completed


async def test_pause_before_ignored_when_autonomous(tmp_path: Path):
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: swarm }
  interaction:
    autonomy: autonomous
    guards: [{ pauseBefore: [Bash] }]
  agents: [{ name: a, harness: sdk }]
""")
    coord = Coordinator(team, project_dir=tmp_path, dry_run=False)
    res = await coord.run("obj")
    assert res.completed  # autonomous never pauses for humans


async def test_attach_session_inject_interrupt_detach(lead_crew: Path):
    """The attach seam is real: stream + inject + interrupt + detach (§6.2/§13)."""
    team = compile_folder(lead_crew).team
    coord = Coordinator(team, project_dir=lead_crew, dry_run=True)
    session = await coord.open_attach("planner")

    turn = await session.inject("focus on the API first")
    assert turn.text  # the adapter produced a reply
    assert session.injected == ["focus on the API first"]

    events = [e async for e in session.stream()]
    assert any(e.type == "human.inject" for e in events)
    assert any(e.type == "turn.delta" for e in events)

    await session.interrupt()
    assert session.interrupted is True

    session.detach()
    assert session.detached is True


async def test_context_accessors_injected(lead_crew: Path):
    """AgentContext carries memory/mailbox/budget accessors (§6.2)."""
    team = compile_folder(lead_crew).team
    coord = Coordinator(team, project_dir=lead_crew, dry_run=True)
    ctx = coord._context("planner")
    assert ctx.memory is coord.memory
    assert ctx.mailbox is coord.mail
    assert ctx.budget is coord.budget


async def test_cluster_pods_do_not_double_plan(tmp_path: Path):
    """Two pods sharing one task store must not duplicate the task graph: only the
    lead pod plans (the multi-pod double-plan guard)."""
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: lead-and-teammates, lead: planner }
  interaction: { autonomy: autonomous }
  agents:
    - { name: planner, harness: sdk }
    - { name: implementer, harness: sdk }
""")
    # A non-lead pod runs first — it must NOT create any tasks.
    pod_impl = Coordinator(team, project_dir=tmp_path, agent="implementer")
    await pod_impl.run("obj")
    assert len(await pod_impl.tasks.list()) == 0

    # The lead pod plans + drives its own work.
    pod_plan = Coordinator(team, project_dir=tmp_path, agent="planner")
    await pod_plan.run("obj")
    after_plan = await pod_plan.tasks.list()
    assert len(after_plan) == 2  # implementer task + planner synthesis, once

    # The implementer pod runs again and claims its task — no new tasks created.
    await pod_impl.run("obj")
    final = await pod_impl.tasks.list()
    assert len(final) == 2  # no duplication
    assert {t.owner for t in final} == {"planner", "implementer"}


async def test_resume_after_partial_run_completes(tmp_path: Path):
    """Kill mid-run (max_turns) then resume from persisted state → completes (the
    durable-resume invariant; the production form of kill-all-pods → reconcile)."""
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
    first = await Coordinator(team, project_dir=tmp_path, max_turns=1).run("obj")
    assert not first.completed  # stopped early

    # A fresh Coordinator (simulating a restart) resumes the persisted tasks.
    resumed = await Coordinator(team, project_dir=tmp_path).run("obj")
    assert resumed.completed
    assert all(t.state == "done" for t in resumed.tasks)


async def test_budget_pause_resume_does_not_fake_guard(tmp_path: Path):
    """A budget pause is re-gated on resume — release_paused must NOT mark it as a
    satisfied guard (the two halt reasons are distinct)."""
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  budgets: { team: { provider: anthropic, limit: 1, window: session, onExceed: pause } }
  coordination: { pattern: swarm }
  interaction: { autonomy: supervised }
  agents: [{ name: a, harness: sdk }]
""")
    coord = Coordinator(team, project_dir=tmp_path)
    res = await coord.run("a long objective exceeding one token")
    paused = [t for t in res.tasks if t.state == "paused"]
    assert paused and paused[0].pause_reason == "budget"
    await coord.release_paused()
    t = (await coord.tasks.list())[0]
    assert t.state == "pending"
    assert t.guard_released is False  # budget pause did not fake a guard release


async def test_review_state_is_entered(tmp_path: Path):
    """Assert the task actually transitions through `review` (event evidence)."""
    seen: list[tuple] = []
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: lead-and-teammates, lead: planner }
  interaction: { autonomy: autonomous }
  agents:
    - { name: planner, harness: sdk }
    - { name: implementer, harness: sdk }
    - { name: reviewer, harness: sdk }
""")
    coord = Coordinator(team, project_dir=tmp_path,
                        on_event=lambda e: seen.append((e.type, e.fields.get("to_state"))))
    await coord.run("build it")
    assert ("task.transition", "review") in seen  # the review state was entered


async def test_direct_dispatch_drives_single_agent(tmp_path: Path):
    """`target_agent` (the local `karo run --agent` path) creates ONE task owned by
    the named agent and bypasses the lead decomposition."""
    team = _flat(tmp_path, """\
metadata: { name: t }
spec:
  defaults: { permissionMode: bypass }
  coordination: { pattern: lead-and-teammates, lead: planner }
  interaction: { autonomy: autonomous }
  agents:
    - { name: planner, harness: sdk }
    - { name: implementer, harness: sdk }
    - { name: deploy-approver, harness: sdk }
""")
    coord = Coordinator(team, project_dir=tmp_path, target_agent="deploy-approver")
    res = await coord.run("Approve deploy for JIRA-789")
    assert res.completed
    # Exactly one task, owned by the targeted agent — no planner/implementer tasks.
    assert len(res.tasks) == 1
    assert res.tasks[0].owner == "deploy-approver"
    assert res.tasks[0].state == "done"


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
