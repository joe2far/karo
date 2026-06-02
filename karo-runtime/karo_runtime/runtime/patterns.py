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

    # lead-and-teammates (default): one task per teammate, owned by them.
    lead = team.spec.coordination.lead
    teammates = [a for a in agents if a != lead] or agents
    return [
        Task(
            objective=objective,
            owner=mate,
            acceptance_criteria=[f"{mate} contribution to: {objective[:60]}"],
        )
        for mate in teammates
    ]
