"""The Coordinator — the durable runtime heart (CLI §6.1).

Owns the task layer, mailbox delivery, attach/guard gating, memory, and the
authoritative budget gate. Drives agents until tasks reach a terminal state or a
budget/guard/limit halt, persisting every transition to ``.karo/`` so runs are
resumable (``karo run --resume``).

This is shared ``karo-runtime`` code: the operator's Dispatcher/agent pods run
the same loop against Redis/Postgres stores instead of file stores.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from ..harness import AgentContext, get_adapter
from ..harness.base import Message as HarnessMessage
from ..spec.models import AgentTeam, Autonomy, PauseOn
from ..stores.base import Task, TaskState, TERMINAL_STATES
from ..stores.file import FileMailboxStore, FileMemoryStore, FileTaskStore
from .budget import FileBudgetMeter, OnExceed
from .events import EventLog, EventType
from .patterns import plan_tasks


@dataclass
class RunResult:
    run_id: str
    tasks: list[Task]
    halted: bool = False
    halt_reason: str = ""
    paused_agents: list[str] = field(default_factory=list)

    @property
    def completed(self) -> bool:
        return all(t.state in {s.value for s in TERMINAL_STATES} for t in self.tasks)


class Coordinator:
    def __init__(
        self,
        team: AgentTeam,
        *,
        project_dir: str | Path = ".",
        run_id: Optional[str] = None,
        dry_run: bool = False,
        autonomy_override: Optional[str] = None,
        max_turns: Optional[int] = None,
        on_event=None,
    ):
        self.team = team
        self.dir = Path(project_dir)
        self.run_id = run_id or f"run-{uuid.uuid4().hex[:10]}"
        self.dry_run = dry_run
        self.autonomy_override = autonomy_override
        self.max_turns = max_turns
        self._agents = {a.name: a for a in team.spec.agents}

        karo = self.dir / ".karo"
        coord = team.spec.coordination
        self.tasks = FileTaskStore(self._resolve(coord.task_layer.path))
        self.mail = FileMailboxStore(self._resolve(coord.mailbox.path), hard_limit=coord.mailbox.hard_limit)
        mem_path = team.spec.memory.path if team.spec.memory else "./.karo/memory"
        self.memory = FileMemoryStore(self._resolve(mem_path))
        self.events = EventLog(self.run_id, root=karo / "runs", on_event=on_event)

        budgets = team.spec.budgets
        if budgets and budgets.team:
            self.budget = FileBudgetMeter(
                karo / "usage.json",
                limit=budgets.team.limit,
                window=budgets.team.window,
                on_exceed=budgets.team.on_exceed,
                usage_log=karo / "usage.log",
            )
            self.budget_provider = budgets.team.provider
        else:
            self.budget = FileBudgetMeter(karo / "usage.json")
            self.budget_provider = team.spec.defaults.model.provider if team.spec.defaults.model else "anthropic"

    def _resolve(self, p: str) -> Path:
        path = Path(p)
        if path.is_absolute():
            return path
        # Strip a single leading "./" prefix (not a char set — must keep the
        # leading dot of ".karo").
        rel = p[2:] if p.startswith("./") else p
        return self.dir / rel

    # -- planning --------------------------------------------------------- #
    async def plan(self, objective: str) -> list[Task]:
        existing = await self.tasks.list()
        if existing:  # resume: keep persisted tasks
            return existing
        created = []
        for task in plan_tasks(self.team, objective):
            created.append(await self.tasks.create(task))
            self.events.emit(
                EventType.task_transition.value,
                agent=task.owner,
                task_id=task.id,
                from_state="(new)",
                to_state=task.state,
            )
        return created

    def _autonomy(self, agent_name: str) -> str:
        agent = self._agents.get(agent_name)
        if self.autonomy_override:
            return self.autonomy_override
        if agent and agent.interaction and agent.interaction.autonomy:
            return agent.interaction.autonomy
        return self.team.spec.interaction.autonomy

    def _guards(self, agent_name: str):
        agent = self._agents.get(agent_name)
        if agent and agent.interaction and agent.interaction.guards:
            return agent.interaction.guards
        return self.team.spec.interaction.guards

    def _context(self, agent_name: str) -> AgentContext:
        agent = self._agents[agent_name]
        defaults = self.team.spec.defaults
        model = agent.model.model_dump(by_alias=True) if agent.model else (
            defaults.model.model_dump(by_alias=True) if defaults.model else {"provider": "anthropic", "id": "claude-opus-4-8"}
        )
        return AgentContext(
            agent_name=agent_name,
            instructions=agent.instructions,
            model=model,
            tools=agent.tools,
            mcp=agent.mcp,
            skills=agent.skills,
            working_dir=defaults.working_dir,
            permission_mode=(agent.permission_mode or defaults.permission_mode),
            # Accessors the adapter/attach session use (CLI §6.2).
            memory=self.memory,
            mailbox=self.mail,
            budget=self.budget,
        )

    def _harness_for(self, agent_name: str) -> str:
        agent = self._agents[agent_name]
        return agent.harness or self.team.spec.defaults.harness

    async def _transition(self, task: Task, to_state: str) -> None:
        frm = task.state
        task.state = to_state
        await self.tasks.update(task)
        self.events.emit(
            EventType.task_transition.value,
            agent=task.owner,
            task_id=task.id,
            from_state=frm,
            to_state=to_state,
        )

    # -- run -------------------------------------------------------------- #
    async def run(self, objective: str) -> RunResult:
        tasks = await self.plan(objective)
        if self.dry_run:
            return RunResult(self.run_id, tasks)

        paused: list[str] = []
        turns = 0
        halted = False
        halt_reason = ""

        # Drive until no claimable work remains.
        while True:
            task = await self._next_runnable()
            if task is None:
                break
            if self.max_turns is not None and turns >= self.max_turns:
                halted, halt_reason = True, "max-turns"
                break

            owner = task.owner or await self._assign_swarm(task)
            ctx = self._context(owner)

            # Guard: pauseBefore — pause before acting and await human attach
            # (supervised only). Released by `karo attach`/continue (§13).
            pb = self._pause_before(owner)
            if pb and not task.guard_released and self._autonomy(owner) != Autonomy.autonomous.value:
                await self._transition(task, TaskState.paused.value)
                paused.append(owner)
                self.events.emit(EventType.guard_pause.value, agent=owner,
                                 guard=f"pauseBefore:{','.join(pb)}",
                                 reason=f"pauseBefore:{','.join(pb)}")
                continue

            # Authoritative budget gate before the turn (§8).
            est = max(1, len(ctx.instructions + task.objective) // 4)
            decision = self.budget.can_spend(self.budget_provider, est, owner)
            if not decision.allowed:
                self.events.emit(
                    EventType.budget_halt.value, agent=owner,
                    provider=self.budget_provider, mode=decision.mode,
                    used=decision.used, limit=decision.limit,
                )
                if decision.mode == OnExceed.hardstop.value:
                    halted, halt_reason = True, "budget-hardstop"
                    break
                if decision.mode == OnExceed.pause.value:
                    await self._transition(task, TaskState.paused.value)
                    paused.append(owner)
                    self.events.emit(EventType.guard_pause.value, agent=owner,
                                     guard="budget", reason="budget")
                    continue
                # warn: fall through and continue.

            await self._transition(task, TaskState.in_progress.value)
            self.events.emit(EventType.turn_start.value, agent=owner,
                             turn_id=f"t{turns}", task_id=task.id)

            adapter = get_adapter(self._harness_for(owner), dry_run=self.dry_run)
            result = await adapter.run_turn(ctx, HarnessMessage("user", task.objective))
            turns += 1

            self.budget.record(self.budget_provider, owner,
                               result.prompt_tokens, result.completion_tokens, result.estimated)
            self.events.emit(EventType.model_usage.value, agent=owner,
                             provider=self.budget_provider,
                             prompt_tokens=result.prompt_tokens,
                             completion_tokens=result.completion_tokens,
                             estimated=result.estimated)
            self.events.emit(EventType.turn_end.value, agent=owner,
                             turn_id=f"t{turns - 1}", status="ok",
                             tokens=result.completion_tokens)

            task.result = {"text": result.text}
            task.attempts += 1
            await self.memory.put("team", f"task:{task.id}", result.text, tags=[owner])

            # Guard: pauseOn taskComplete (supervised only).
            if self._should_pause_on(owner, PauseOn.task_complete.value):
                await self._transition(task, TaskState.paused.value)
                paused.append(owner)
                self.events.emit(EventType.guard_pause.value, agent=owner,
                                 guard="pauseOn:taskComplete", reason="pauseOn:taskComplete")
                continue

            await self._transition(task, TaskState.done.value)

        all_tasks = await self.tasks.list()
        return RunResult(
            self.run_id, all_tasks,
            halted=halted, halt_reason=halt_reason,
            paused_agents=sorted(set(paused)),
        )

    def _pause_before(self, agent_name: str) -> list[str]:
        """Tool names that should pause the agent before invocation (§13)."""
        tools: list[str] = []
        for g in self._guards(agent_name):
            if g.pause_before:
                tools.extend(g.pause_before)
        return tools

    # -- attach & direct (§13) ------------------------------------------- #
    async def open_attach(self, agent_name: str):
        """Open a live attach session to an agent (stream+inject+interrupt+detach)."""
        ctx = self._context(agent_name)
        adapter = get_adapter(self._harness_for(agent_name), dry_run=self.dry_run)
        self.events.emit(EventType.attach.value, agent=agent_name, user="local")
        return await adapter.attach(ctx)

    async def release_paused(self, agent: Optional[str] = None) -> list[str]:
        """Release guard-paused tasks back to pending so the next run continues.

        Used by `karo attach ... --continue`. Marks ``guard_released`` so the
        pauseBefore guard does not re-trip (persisted for cross-process resume).
        """
        released: list[str] = []
        for t in await self.tasks.list(state=TaskState.paused.value):
            if agent and t.owner != agent:
                continue
            t.guard_released = True
            await self._transition(t, TaskState.pending.value)
            released.append(t.id)
        return released

    def _should_pause_on(self, agent: str, event: str) -> bool:
        if self._autonomy(agent) == Autonomy.autonomous.value:
            return False
        for g in self._guards(agent):
            if g.pause_on == event:
                return True
        return False

    async def _next_runnable(self) -> Optional[Task]:
        tasks = sorted(await self.tasks.list(), key=lambda t: t.created)
        done = {t.id for t in tasks if t.state == TaskState.done.value}
        for t in tasks:
            if t.state in {TaskState.pending.value, TaskState.assigned.value}:
                if all(dep in done for dep in t.depends_on):
                    return t
        return None

    async def _assign_swarm(self, task: Task) -> str:
        # Fallback owner for unowned (swarm) tasks: first agent.
        owner = self.team.spec.agents[0].name
        task.owner = owner
        await self.tasks.update(task)
        return owner
