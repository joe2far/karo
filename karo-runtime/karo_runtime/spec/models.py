"""Pydantic models for the compiled ``AgentTeam`` spec.

These models are the single in-memory representation of the *compiled* form
(``team.yaml``) described in ``PRD-KARO-CLI.md`` §4.2. They are shared by the
CLI and (via ``karo-runtime``) the operator's agent image, so they MUST NOT
diverge between the two lanes.

The on-disk/interchange form uses ``camelCase`` for several fields
(``perAgent``, ``onExceed``, ``pauseBefore`` …). We model those with field
aliases and ``populate_by_name=True`` so both the Python attribute name and the
wire name are accepted.
"""

from __future__ import annotations

from enum import Enum
from typing import Any, Literal, Optional, Union

from pydantic import BaseModel, ConfigDict, Field, model_validator

# The ONLY accepted apiVersion in this release (CLI §4.4, v2 §3). Mirrored in
# the operator. Anything else is rejected with an actionable upgrade message.
ACCEPTED_API_VERSIONS: tuple[str, ...] = ("karo.dev/v1",)
DEFAULT_API_VERSION = "karo.dev/v1"


class _Base(BaseModel):
    """Common config: accept aliases, forbid unknown fields to catch typos."""

    model_config = ConfigDict(
        populate_by_name=True,
        extra="forbid",
        use_enum_values=True,
    )


# --------------------------------------------------------------------------- #
# Enums
# --------------------------------------------------------------------------- #
class Harness(str, Enum):
    sdk = "sdk"
    claude_code = "claude-code"
    cursor = "cursor"
    codex = "codex"


class Provider(str, Enum):
    anthropic = "anthropic"
    bedrock = "bedrock"
    vertex = "vertex"


class PermissionMode(str, Enum):
    prompt = "prompt"
    accept_edits = "acceptEdits"
    plan = "plan"
    bypass = "bypass"


class Window(str, Enum):
    daily = "daily"
    session = "session"
    unbounded = "unbounded"


class OnExceed(str, Enum):
    warn = "warn"
    pause = "pause"
    hardstop = "hardstop"


class MemoryScope(str, Enum):
    team = "team"
    per_agent = "per-agent"
    both = "both"


class GcStrategy(str, Enum):
    aggressive = "aggressive"
    lru = "lru"
    none = "none"


class Pattern(str, Enum):
    lead_and_teammates = "lead-and-teammates"
    pipeline = "pipeline"
    swarm = "swarm"


class Autonomy(str, Enum):
    supervised = "supervised"
    autonomous = "autonomous"


class PauseOn(str, Enum):
    task_complete = "taskComplete"
    plan_ready = "planReady"
    error = "error"


class Transport(str, Enum):
    stdio = "stdio"
    http = "http"


class Backend(str, Enum):
    file = "file"
    sqlite = "sqlite"
    redis = "redis"
    none = "none"


# --------------------------------------------------------------------------- #
# Model binding
# --------------------------------------------------------------------------- #
class ModelBinding(_Base):
    provider: Provider = Provider.anthropic
    id: str
    profile: Optional[str] = None  # explicit credential profile (local-only, §14)
    params: dict[str, Any] = Field(default_factory=dict)


# --------------------------------------------------------------------------- #
# Budgets (§8)
# --------------------------------------------------------------------------- #
class TeamBudget(_Base):
    provider: Provider = Provider.anthropic
    limit: int  # plain integer; no underscores (§4.0 numeric-literals rule)
    window: Window = Window.daily
    on_exceed: OnExceed = Field(default=OnExceed.pause, alias="onExceed")


class Budgets(_Base):
    team: Optional[TeamBudget] = None
    per_agent: bool = Field(default=False, alias="perAgent")


class AgentBudget(_Base):
    """Per-agent override: either an explicit token ``limit`` or a ``share``."""

    share: Optional[float] = None
    limit: Optional[int] = None

    @model_validator(mode="after")
    def _exactly_one(self) -> "AgentBudget":
        if (self.share is None) == (self.limit is None):
            raise ValueError("agent budget must set exactly one of {share, limit}")
        return self


# --------------------------------------------------------------------------- #
# Resources (§9)
# --------------------------------------------------------------------------- #
class McpServer(_Base):
    name: str
    transport: Transport = Transport.stdio
    command: Optional[list[str]] = None  # stdio
    env: dict[str, str] = Field(default_factory=dict)
    url: Optional[str] = None  # http
    headers: dict[str, str] = Field(default_factory=dict)

    @model_validator(mode="after")
    def _transport_fields(self) -> "McpServer":
        if self.transport == Transport.stdio.value and not self.command:
            raise ValueError(f"mcp server {self.name!r}: stdio transport requires 'command'")
        if self.transport == Transport.http.value and not self.url:
            raise ValueError(f"mcp server {self.name!r}: http transport requires 'url'")
        return self


class SkillRef(_Base):
    source: str  # ./skills/<name> or pack:owner/name


class ToolDef(_Base):
    name: str
    module: str  # path:function
    description: Optional[str] = None
    schema_: dict[str, Any] = Field(default_factory=dict, alias="schema")


class Repo(_Base):
    """A git repository an agent works on (§9).

    Declared once under ``resources.repos`` and referenced by name from an
    agent's ``repos:`` list. Locally the CLI clones/checks it out into the
    workspace before a run; on cluster the agent pod's init-container does the
    same from the same spec. ``ref`` is a branch, tag, or commit SHA (default:
    the remote's default branch). ``path`` overrides the checkout location
    (default ``<workingDir>/<name>``).
    """
    name: str
    url: str
    ref: Optional[str] = None
    path: Optional[str] = None


class Resources(_Base):
    mcp_servers: list[McpServer] = Field(default_factory=list, alias="mcpServers")
    skills: list[SkillRef] = Field(default_factory=list)
    tools: list[ToolDef] = Field(default_factory=list)
    repos: list[Repo] = Field(default_factory=list)


# --------------------------------------------------------------------------- #
# Memory (§10)
# --------------------------------------------------------------------------- #
class Retention(_Base):
    max_items: Optional[int] = Field(default=None, alias="maxItems")
    gc_strategy: GcStrategy = Field(default=GcStrategy.lru, alias="gcStrategy")


class Memory(_Base):
    backend: Backend = Backend.file
    path: str = "./.karo/memory"
    scope: MemoryScope = MemoryScope.team
    retention: Retention = Field(default_factory=Retention)


# --------------------------------------------------------------------------- #
# Coordination (§11)
# --------------------------------------------------------------------------- #
class Pipeline(_Base):
    stages: list[str] = Field(default_factory=list)


class MailboxConfig(_Base):
    backend: Backend = Backend.file
    path: str = "./.karo/mail"
    hard_limit: int = Field(default=500, alias="hardLimit")


class TaskLayer(_Base):
    backend: Backend = Backend.file
    path: str = "./.karo/tasks"
    resumable: bool = True


class Coordination(_Base):
    pattern: Pattern = Pattern.lead_and_teammates
    lead: Optional[str] = None
    reviewer: Optional[str] = None  # explicit reviewer agent (else the "reviewer"-named one)
    pipeline: Optional[Pipeline] = None
    mailbox: MailboxConfig = Field(default_factory=MailboxConfig)
    task_layer: TaskLayer = Field(default_factory=TaskLayer, alias="taskLayer")


# --------------------------------------------------------------------------- #
# Interaction (§13)
# --------------------------------------------------------------------------- #
class Guard(_Base):
    pause_before: Optional[list[str]] = Field(default=None, alias="pauseBefore")
    pause_on: Optional[PauseOn] = Field(default=None, alias="pauseOn")

    @model_validator(mode="after")
    def _exactly_one(self) -> "Guard":
        if (self.pause_before is None) == (self.pause_on is None):
            raise ValueError("guard must set exactly one of {pauseBefore, pauseOn}")
        return self


class Interaction(_Base):
    attachable: bool = True
    autonomy: Autonomy = Autonomy.supervised
    guards: list[Guard] = Field(default_factory=list)
    pause_timeout: int = Field(default=0, alias="pauseTimeout")  # 0 = wait forever


# --------------------------------------------------------------------------- #
# Defaults (§4.2)
# --------------------------------------------------------------------------- #
class Defaults(_Base):
    harness: Harness = Harness.sdk
    model: Optional[ModelBinding] = None
    permission_mode: PermissionMode = Field(
        default=PermissionMode.prompt, alias="permissionMode"
    )
    working_dir: str = Field(default="./workspace", alias="workingDir")


# --------------------------------------------------------------------------- #
# Agent (§4.2)
# --------------------------------------------------------------------------- #
class AgentMemoryRef(_Base):
    scope: MemoryScope = MemoryScope.team


class Agent(_Base):
    name: str
    instructions: str = ""
    harness: Optional[Harness] = None
    model: Optional[ModelBinding] = None
    tools: list[str] = Field(default_factory=list)
    mcp: list[str] = Field(default_factory=list)
    skills: list[str] = Field(default_factory=list)
    repos: list[str] = Field(default_factory=list)
    memory: Optional[AgentMemoryRef] = None
    mailbox: Optional[str] = None  # address; defaults to agent name
    interaction: Optional[Interaction] = None
    budget: Optional[AgentBudget] = None
    permission_mode: Optional[PermissionMode] = Field(default=None, alias="permissionMode")


# --------------------------------------------------------------------------- #
# Top-level spec / document
# --------------------------------------------------------------------------- #
class Metadata(_Base):
    name: str
    namespace: Optional[str] = None  # set by `karo export --namespace`; outside parity
    labels: dict[str, str] = Field(default_factory=dict)


class AgentTeamSpec(_Base):
    defaults: Defaults = Field(default_factory=Defaults)
    budgets: Optional[Budgets] = None
    resources: Resources = Field(default_factory=Resources)
    memory: Optional[Memory] = None
    coordination: Coordination = Field(default_factory=Coordination)
    interaction: Interaction = Field(default_factory=Interaction)
    agents: list[Agent] = Field(default_factory=list)


class AgentTeam(_Base):
    """The compiled ``AgentTeam`` document (``team.yaml`` form)."""

    api_version: str = Field(default=DEFAULT_API_VERSION, alias="apiVersion")
    kind: Literal["AgentTeam"] = "AgentTeam"
    metadata: Metadata
    spec: AgentTeamSpec = Field(default_factory=AgentTeamSpec)

    # ``runtime:`` is added only by ``karo export`` and consumed only by the
    # operator. It is kept as opaque data here so the CLI never reasons about it
    # and it stays out of the shared ``spec`` parity comparison.
    runtime: Optional[dict[str, Any]] = None

    @model_validator(mode="after")
    def _check_api_version(self) -> "AgentTeam":
        if self.api_version not in ACCEPTED_API_VERSIONS:
            accepted = ", ".join(ACCEPTED_API_VERSIONS)
            raise ValueError(
                f"unsupported apiVersion {self.api_version!r}; "
                f"this release accepts only: {accepted}. "
                f"Upgrade the karo CLI/operator to a release that supports it."
            )
        return self


# Convenience union used by a couple of helpers.
SpecModel = Union[AgentTeam, AgentTeamSpec]
