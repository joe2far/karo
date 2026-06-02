"""Static validation of the compiled ``AgentTeam`` (CLI §4.3, §7).

No network, no model calls. Performs schema-shaped checks (already partly
enforced by pydantic on construction) plus the cross-field / cross-reference
rules that the shared JSON Schema expresses as ``if/then`` so the operator's CRD
validation agrees. Diagnostics map back to the *source folder file* where
possible.
"""

from __future__ import annotations

import fnmatch
import re
from dataclasses import asdict, dataclass
from typing import Any, Optional

from .compile import CompileResult
from .models import (
    AgentTeam,
    Autonomy,
    Harness,
    Pattern,
    PauseOn,
    PermissionMode,
)

# Built-in tools available from the sdk harness (CLI §9). Guard matchers may
# reference these by exact name or glob.
BUILTIN_TOOLS: frozenset[str] = frozenset(
    {"Read", "Write", "Edit", "Bash", "Grep", "Glob", "WebFetch", "WebSearch"}
)

# Harnesses that can run as a headless cluster pod (CLI §4.7).
CLUSTER_CAPABLE_HARNESSES: frozenset[str] = frozenset({Harness.sdk.value})

_DNS1123 = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
_GLOB_CHARS = re.compile(r"[*?\[\]]")
_MCP_MATCHER = re.compile(r"^mcp:([^/]+)/(.+)$")


@dataclass
class Diagnostic:
    file: str
    line: int
    severity: str  # error | warning
    code: str
    message: str

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


class Validator:
    def __init__(self, result: CompileResult, *, target: str = "local"):
        self.result = result
        self.team: AgentTeam = result.team
        self.target = target
        self.diags: list[Diagnostic] = []

    # -- helpers ---------------------------------------------------------- #
    def _src(self, key: str) -> str:
        return self.result.source_map.get(key, "karo.yaml")

    def _err(self, code: str, message: str, *, src: str = "karo.yaml", line: int = 0):
        self.diags.append(Diagnostic(src, line, "error", code, message))

    def _warn(self, code: str, message: str, *, src: str = "karo.yaml", line: int = 0):
        self.diags.append(Diagnostic(src, line, "warning", code, message))

    # -- checks ----------------------------------------------------------- #
    def run(self) -> list[Diagnostic]:
        self._check_metadata()
        self._check_agents()
        self._check_resource_refs()
        self._check_coordination()
        self._check_guards()
        self._check_budgets()
        self._check_permission_autonomy()
        if self.target == "cluster":
            self._check_cluster()
        return self.diags

    def _check_metadata(self) -> None:
        name = self.team.metadata.name
        if not name or not _DNS1123.match(name) or len(name) > 63:
            self._err(
                "metadata-name",
                f"metadata.name {name!r} must be DNS-1123 (lowercase alphanumeric "
                f"and '-', <=63 chars)",
            )

    def _check_agents(self) -> None:
        agents = self.team.spec.agents
        if not agents:
            self._err("no-agents", "spec.agents must contain at least one agent")
            return
        seen: set[str] = set()
        for a in agents:
            if a.name in seen:
                self._err(
                    "duplicate-agent",
                    f"duplicate agent name {a.name!r}",
                    src=self._src(f"agent:{a.name}"),
                )
            seen.add(a.name)

    def _check_resource_refs(self) -> None:
        res = self.team.spec.resources
        tool_names = {t.name for t in res.tools}
        skill_names = {s.source.rstrip("/").split("/")[-1] for s in res.skills}
        mcp_names = {m.name for m in res.mcp_servers}
        repo_names = {r.name for r in res.repos}
        for a in self.team.spec.agents:
            src = self._src(f"agent:{a.name}")
            for t in a.tools:
                if t not in tool_names and t not in BUILTIN_TOOLS:
                    self._err(
                        "unknown-tool",
                        f"agent {a.name!r} references unknown tool {t!r}",
                        src=src,
                    )
            for s in a.skills:
                if s not in skill_names:
                    self._err(
                        "unknown-skill",
                        f"agent {a.name!r} references unknown skill {s!r}",
                        src=src,
                    )
            for m in a.mcp:
                if m not in mcp_names:
                    self._err(
                        "unknown-mcp",
                        f"agent {a.name!r} references unknown mcp server {m!r}",
                        src=src,
                    )
            for r in a.repos:
                if r not in repo_names:
                    self._err(
                        "unknown-repo",
                        f"agent {a.name!r} references unknown repo {r!r}",
                        src=src,
                    )

    def _check_coordination(self) -> None:
        coord = self.team.spec.coordination
        agent_names = {a.name for a in self.team.spec.agents}
        pattern = coord.pattern

        if coord.reviewer and coord.reviewer not in agent_names:
            self._err(
                "reviewer-unknown",
                f"coordination.reviewer {coord.reviewer!r} does not name an existing agent",
            )

        if pattern == Pattern.lead_and_teammates.value:
            if not coord.lead:
                self._err(
                    "lead-required",
                    "coordination.lead is required when pattern is 'lead-and-teammates'",
                )
            elif coord.lead not in agent_names:
                self._err(
                    "lead-unknown",
                    f"coordination.lead {coord.lead!r} does not name an existing agent",
                )
        else:
            if coord.lead:
                self._err(
                    "lead-unexpected",
                    f"coordination.lead is only valid for 'lead-and-teammates', "
                    f"not {pattern!r}",
                )

        if pattern == Pattern.pipeline.value:
            stages = coord.pipeline.stages if coord.pipeline else []
            if not stages:
                self._err(
                    "pipeline-required",
                    "coordination.pipeline.stages is required when pattern is 'pipeline'",
                )
            else:
                for stage in stages:
                    if stage not in agent_names:
                        self._err(
                            "pipeline-unknown",
                            f"pipeline stage {stage!r} does not name an existing agent",
                        )
                if len(stages) != len(set(stages)):
                    self._err(
                        "pipeline-cycle",
                        "coordination.pipeline.stages must be acyclic (no repeats)",
                    )
        elif coord.pipeline and coord.pipeline.stages:
            self._err(
                "pipeline-unexpected",
                f"coordination.pipeline.stages is only valid for 'pipeline', "
                f"not {pattern!r}",
            )

    def _resolve_matcher(self, matcher: str) -> bool:
        """True if a guard ``pauseBefore`` matcher resolves to a known tool."""
        res = self.team.spec.resources
        known = set(BUILTIN_TOOLS) | {t.name for t in res.tools}
        mcp_names = {m.name for m in res.mcp_servers}

        mcp = _MCP_MATCHER.match(matcher)
        if mcp:
            return mcp.group(1) in mcp_names
        if _GLOB_CHARS.search(matcher):
            return any(fnmatch.fnmatch(name, matcher) for name in known)
        return matcher in known

    def _check_guards(self) -> None:
        def check_guards(guards, src: str) -> None:
            for g in guards:
                if g.pause_before:
                    for matcher in g.pause_before:
                        if not self._resolve_matcher(matcher):
                            self._err(
                                "guard-matcher",
                                f"guard pauseBefore matcher {matcher!r} resolves to "
                                f"no known built-in/custom/mcp tool",
                                src=src,
                            )
                if g.pause_on and g.pause_on not in {e.value for e in PauseOn}:
                    self._err(
                        "guard-pauseon",
                        f"guard pauseOn {g.pause_on!r} is not a valid lifecycle event",
                        src=src,
                    )

        check_guards(self.team.spec.interaction.guards, "karo.yaml")
        for a in self.team.spec.agents:
            if a.interaction:
                check_guards(a.interaction.guards, self._src(f"agent:{a.name}"))

    def _check_budgets(self) -> None:
        budgets = self.team.spec.budgets
        if not budgets or not budgets.team:
            return
        limit = budgets.team.limit
        if limit <= 0:
            self._err("budget-limit", "budgets.team.limit must be a positive integer")
            return
        explicit = 0
        for a in self.team.spec.agents:
            if a.budget is None:
                continue
            if a.budget.limit is not None:
                explicit += a.budget.limit
            elif a.budget.share is not None:
                explicit += int(round(a.budget.share * limit))
        if explicit > limit:
            self._err(
                "budget-overcommit",
                f"sum of explicit per-agent budgets ({explicit}) exceeds team "
                f"limit ({limit})",
            )

    def _effective_autonomy(self, agent) -> str:
        if agent.interaction and agent.interaction.autonomy:
            return agent.interaction.autonomy
        return self.team.spec.interaction.autonomy

    def _effective_permission_mode(self, agent) -> str:
        return (
            agent.permission_mode
            or self.team.spec.defaults.permission_mode
            or PermissionMode.prompt.value
        )

    def _check_permission_autonomy(self) -> None:
        # prompt + autonomous is invalid: an unattended agent can't block on a
        # prompt (CLI §4.2.1).
        for a in self.team.spec.agents:
            pm = self._effective_permission_mode(a)
            auto = self._effective_autonomy(a)
            if pm == PermissionMode.prompt.value and auto == Autonomy.autonomous.value:
                self._err(
                    "prompt-autonomous",
                    f"agent {a.name!r}: permissionMode 'prompt' with autonomy "
                    f"'autonomous' is invalid (no interactive surface)",
                    src=self._src(f"agent:{a.name}"),
                )

    def _check_cluster(self) -> None:
        for a in self.team.spec.agents:
            harness = a.harness or self.team.spec.defaults.harness
            if harness not in CLUSTER_CAPABLE_HARNESSES:
                self._err(
                    "harness-not-cluster",
                    f"agent {a.name!r}: harness {harness!r} is local-only and cannot "
                    f"run on cluster (only 'sdk' is cluster-capable; CLI §4.7)",
                    src=self._src(f"agent:{a.name}"),
                )
            pm = self._effective_permission_mode(a)
            if pm == PermissionMode.prompt.value:
                self._warn(
                    "prompt-cluster",
                    f"agent {a.name!r}: permissionMode 'prompt' has no interactive "
                    f"surface on cluster; karo export will coerce it to a pauseBefore "
                    f"guard (CLI §4.2.1)",
                    src=self._src(f"agent:{a.name}"),
                )


def validate(
    result: CompileResult,
    *,
    target: str = "local",
    raw_texts: Optional[dict[str, str]] = None,
) -> list[Diagnostic]:
    """Validate a compiled team; ``raw_texts`` maps source file -> text for the
    numeric-literal lint."""
    diags = Validator(result, target=target).run()
    if raw_texts:
        from .loader import lint_numeric_literals

        for src, text in raw_texts.items():
            for d in lint_numeric_literals(text, src):
                diags.append(Diagnostic(**d))
    return diags
