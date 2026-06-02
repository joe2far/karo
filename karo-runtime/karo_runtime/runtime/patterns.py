"""Coordination patterns: how initial tasks are created and claimed (CLI §11).

All three patterns run on the same Coordinator primitives (tasks + mailbox +
memory + attach/guards). The pattern only changes *who creates tasks and how
they're claimed*:

- lead-and-teammates: the lead decomposes the objective into one task per
  teammate (a real model does richer decomposition; the structural fallback
  keeps the runtime deterministic offline).
- pipeline: a fixed sequence; each stage depends on the previous one.
- swarm: an unowned shared queue; agents claim atomically (first-available).
"""

from __future__ import annotations

from ..spec.models import AgentTeam, Pattern
from ..stores.base import Task


def plan_tasks(team: AgentTeam, objective: str) -> list[Task]:
    pattern = team.spec.coordination.pattern
    agents = [a.name for a in team.spec.agents]

    if pattern == Pattern.pipeline.value:
        stages = team.spec.coordination.pipeline.stages if team.spec.coordination.pipeline else agents
        tasks: list[Task] = []
        prev_id: str | None = None
        for stage in stages:
            t = Task(
                objective=objective,
                owner=stage,
                acceptance_criteria=[f"{stage} stage complete"],
                depends_on=[prev_id] if prev_id else [],
            )
            tasks.append(t)
            prev_id = t.id
        return tasks

    if pattern == Pattern.swarm.value:
        # Unowned tasks; agents claim atomically. One seed task per agent here.
        return [
            Task(objective=objective, acceptance_criteria=["claimed and completed"])
            for _ in agents
        ]

    # lead-and-teammates (default): the lead decomposes the objective into one
    # task per implementer teammate; if a `reviewer` agent exists, each task is
    # reviewed (the `review` state) before done; finally the lead synthesizes,
    # depending on every teammate task. Deterministic so parity holds offline.
    lead = team.spec.coordination.lead or agents[0]
    # Explicit reviewer wins; otherwise fall back to the "reviewer"-named agent.
    declared_reviewer = team.spec.coordination.reviewer
    if declared_reviewer and declared_reviewer in agents and declared_reviewer != lead:
        reviewer = declared_reviewer
    else:
        reviewer = next((a for a in agents if a == "reviewer" and a != lead), None)
    implementers = [a for a in agents if a not in {lead, reviewer}] or [
        a for a in agents if a != lead
    ] or agents

    tasks: list[Task] = []
    impl_ids: list[str] = []
    for mate in implementers:
        t = Task(
            objective=objective,
            owner=mate,
            reviewer=reviewer,
            acceptance_criteria=[f"{mate} contribution to: {objective[:60]}"],
        )
        tasks.append(t)
        impl_ids.append(t.id)

    # Lead synthesis task, gated on every teammate task (the lead's report).
    tasks.append(
        Task(
            objective=f"Synthesize teammate results for: {objective}",
            owner=lead,
            depends_on=impl_ids,
            acceptance_criteria=["lead synthesizes teammate contributions"],
        )
    )
    return tasks
