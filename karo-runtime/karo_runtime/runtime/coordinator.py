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
        agent: Optional[str] = None,
        target_agent: Optional[str] = None,
        on_event=None,
    ):
        self.team = team
        self.dir = Path(project_dir)
        self.run_id = run_id or f"run-{uuid.uuid4().hex[:10]}"
        self.dry_run = dry_run
        self.autonomy_override = autonomy_override
        self.max_turns = max_turns
        # When set, this Coordinator drives ONE agent (a cluster pod): it claims
        # only that agent's tasks, and only the lead agent's pod plans — so N pods
        # sharing one Postgres never duplicate the task graph. None = local
        # single-process: plans once and drives all agents.
        self.agent = agent
        # Direct dispatch (the local "sling to one agent" path, CLI §7): when set,
        # the objective becomes a *single* task owned by this agent, bypassing the
        # coordination pattern's lead decomposition. Distinct from `agent` (which
        # is cluster pod-scoping over an already-planned graph).
        self.target_agent = target_agent
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
        # Direct dispatch: one task, owned by the targeted agent, no decomposition.
        if self.target_agent:
            direct = Task(
                objective=objective,
                owner=self.target_agent,
                acceptance_criteria=[f"{self.target_agent} completes: {objective[:60]}"],
            )
            created_task = await self.tasks.create(direct)
            self.events.emit(
                EventType.task_transition.value,
                agent=created_task.owner,
                task_id=created_task.id,
                from_state="(new)",
                to_state=created_task.state,
            )
            return [created_task]
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
        """Drive the team to terminal state via atomic claim + mailbox handoff.

        Each iteration atomically claims the next runnable task (deterministic
        agent order); on cluster every agent pod runs this *same* loop scoped to
        its own agent, so file (local) and Postgres (cluster) produce an identical
        task graph — the parity invariant (CLI §11, v2 §6).
        """
        lead = self.team.spec.coordination.lead
        planner_agent = lead or (self.team.spec.agents[0].name if self.team.spec.agents else None)

        # Leader-elected planning: a cluster pod only plans if it is the lead
        # (planner); all other pods just claim. Local (agent=None) always plans.
        if self.agent is None or self.agent == planner_agent:
            tasks = await self.plan(objective)
        else:
            tasks = await self.tasks.list()
        if self.dry_run:
            return RunResult(self.run_id, tasks)

        paused: list[str] = []
        self._turns = 0
        halted = False
        halt_reason = ""
        # A pod (agent set) claims only its own work; a direct dispatch
        # (target_agent) drives just that agent; otherwise local drives all agents.
        if self.agent:
            agents = [self.agent]
        elif self.target_agent:
            agents = [self.target_agent]
        else:
            agents = [a.name for a in self.team.spec.agents]

        while True:
            if self.max_turns is not None and self._turns >= self.max_turns:
                halted, halt_reason = True, "max-turns"
                break

            # Atomic claim — the swarm/parallel-pull safety guarantee. Owned
            # tasks are claimed by their agent; unowned ones first-available.
            task = None
            for agent in agents:
                task = await self.tasks.claim(owner=agent, agent=agent)
                if task is not None:
                    break
            if task is None:
                break

            owner = task.owner
            ctx = self._context(owner)

            # Guard: pauseBefore (supervised only) — pause before acting, await
            # human attach. Released by `karo attach --continue` (§13).
            pb = self._pause_before(owner)
            if pb and not task.guard_released and self._autonomy(owner) != Autonomy.autonomous.value:
                task.pause_reason = "guard:pauseBefore"
                await self._transition(task, TaskState.paused.value)
                paused.append(owner)
                self.events.emit(EventType.guard_pause.value, agent=owner,
                                 guard=f"pauseBefore:{','.join(pb)}",
                                 reason=f"pauseBefore:{','.join(pb)}")
                continue

            # Authoritative budget gate before the turn (§8).
            ok, decision = self._budget_gate(owner, ctx.instructions + task.objective)
            if not ok:
                if decision.mode == OnExceed.hardstop.value:
                    halted, halt_reason = True, "budget-hardstop"
                    break
                if decision.mode == OnExceed.pause.value:
                    task.pause_reason = "budget"
                    await self._transition(task, TaskState.paused.value)
                    paused.append(owner)
                    self.events.emit(EventType.guard_pause.value, agent=owner,
                                     guard="budget", reason="budget")
                    continue
                # warn: fall through.

            # Mailbox handoff: the task is assigned to its owner.
            await self._send_mail(to=owner, sender=(lead or "coordinator"),
                                  body=f"assigned: {task.objective}")

            await self._transition(task, TaskState.in_progress.value)
            result = await self._turn(owner, ctx, task.objective)
            task.result = {"text": result.text}
            task.attempts += 1
            await self.memory.put("team", f"task:{task.id}", result.text, tags=[owner])

            # Report back to the lead (teammate → lead handoff).
            if lead and owner != lead:
                await self._send_mail(to=lead, sender=owner,
                                      body=f"{owner} completed: {result.text[:80]}")

            # Review state: a reviewer agent reviews the work before done (M2).
            if task.reviewer and task.reviewer != owner:
                await self._transition(task, TaskState.review.value)
                rctx = self._context(task.reviewer)
                review = await self._turn(task.reviewer, rctx,
                                          f"Review against acceptance criteria: {result.text[:120]}")
                await self.memory.put("team", f"review:{task.id}", review.text,
                                      tags=[task.reviewer])
                await self._send_mail(to=(lead or task.reviewer), sender=task.reviewer,
                                      body=f"reviewed {task.id}")

            # Guard: pauseOn taskComplete (supervised only).
            if self._should_pause_on(owner, PauseOn.task_complete.value):
                task.pause_reason = "guard:pauseOn"
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

    # -- run helpers ------------------------------------------------------ #
    def _budget_gate(self, owner: str, text: str):
        """Authoritative check-and-reserve before a turn (§8). Returns (ok, decision)."""
        est = max(1, len(text) // 4)
        decision = self.budget.can_spend(self.budget_provider, est, owner)
        if not decision.allowed:
            self.events.emit(EventType.budget_halt.value, agent=owner,
                             provider=self.budget_provider, mode=decision.mode,
                             used=decision.used, limit=decision.limit)
            return False, decision
        return True, decision

    async def _turn(self, owner: str, ctx: AgentContext, message_text: str):
        """Run one agent turn through its harness adapter, metering + eventing."""
        self.events.emit(EventType.turn_start.value, agent=owner, turn_id=f"t{self._turns}")
        adapter = get_adapter(self._harness_for(owner), dry_run=self.dry_run)
        result = await adapter.run_turn(ctx, HarnessMessage("user", message_text))
        self._turns += 1
        self.budget.record(self.budget_provider, owner,
                           result.prompt_tokens, result.completion_tokens, result.estimated)
        self.events.emit(EventType.model_usage.value, agent=owner,
                         provider=self.budget_provider,
                         prompt_tokens=result.prompt_tokens,
                         completion_tokens=result.completion_tokens,
                         estimated=result.estimated)
        self.events.emit(EventType.turn_end.value, agent=owner,
                         turn_id=f"t{self._turns - 1}", status="ok",
                         tokens=result.completion_tokens)
        return result

    async def _send_mail(self, to: str, sender: str, body: str):
        """Deliver a mailbox message and emit the canonical event (§15)."""
        from ..stores.base import Message as MailMessage

        msg = await self.mail.send(MailMessage(to=to, body=body, sender=sender))
        self.events.emit(EventType.mailbox_send.value, agent=sender,
                         **{"from": sender, "to": to, "msg_id": msg.id})
        return msg

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
            # A guard pause is cleared by the human attaching; a budget pause is
            # simply re-gated on the next run (do not fake a guard release).
            if (t.pause_reason or "").startswith("guard"):
                t.guard_released = True
            t.pause_reason = None
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
