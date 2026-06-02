# KARO CLI — Product Requirements & Implementation Spec

> **Status:** Draft v1 (for build)
> **Audience:** implementers, platform engineers (review)
> **Companion doc:** `PRD-KARO-v2.md` (the Kubernetes operator that consumes what this CLI produces)
> **Review:** see `SPEC-REVIEW.md` for the findings that shaped this revision.

---

## 0. How to read this document

This PRD is written to be **directly buildable**. Section 3 (Concepts), Section 4 (the `AgentTeam` spec) and Section 7 (Command reference) are the contract. Everything else is rationale and guidance. If a detail here conflicts with reality discovered during build, prefer the **shared spec** (Section 4) staying identical between this CLI and KARO v2 — that consistency is the entire product thesis.

The single sentence that defines the product:

> **"Define your agent team once, locally, in portable config. Run it on your laptop today. Push the exact same definition to Kubernetes (KARO v2) tomorrow — no rewrite."**

**Two vocabulary rules used throughout (see §4.0/§4.1):**

- **`karo.yaml`** = the *thin folder manifest* you author at a project root (team-wide concerns + references). It is **not** the whole team.
- **`team.yaml`** = the *flat / compiled single-file* form of an `AgentTeam` (the `karo init --flat` output, the `karo run --team` input, and the canonical body of a `karo export`). When this doc says "the AgentTeam" it means the compiled model, regardless of which on-disk form produced it.

---

## 1. Problem statement

Developers now have multiple coding/agent harnesses (Claude Code, Cursor, Codex) and multiple model backends (Anthropic API, AWS Bedrock, Google Vertex AI), each with their own token budgets and config conventions. There is **no portable, shareable, version-controllable way to define a *team* of agents** — roles, tools, memory, task flow, where a human can attach and steer — that:

1. Runs **locally** for fast iteration.
2. Works across **multiple harnesses** without rewriting.
3. Lets you **swap models/providers** for cost or capability reasons.
4. Can be **shared as code** across a team (not trapped in one tool's UI or one person's machine).
5. Graduates **unchanged** to a production Kubernetes runtime.

### What exists and why it doesn't close the gap

- **Claude Code "Agent Teams"** (experimental, `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`): real multi-agent coordination, but **locked inside the Claude Code session** — not exportable, not portable, not shareable as a versioned artifact, not deployable to Kubernetes.
- **Claude Agent SDK**: excellent **single-agent** primitives (agent loop, tools, hooks, in-process MCP). Explicitly provides **no** durable execution, no cross-session persistence, and no multi-agent coordination beyond spawning subagents as tools.
- **Gas Town / Gas City** (Steve Yegge / Chris Sells): strong multi-agent orchestration primitives (work routing, formulas, mailboxes, durable "beads"), but **opinionated and heavyweight**, with uncertain broad adoption and its own organizational metaphors that not every team wants to inherit.

KARO CLI occupies the gap: **portable, harness-agnostic, model-agnostic team definition + local runtime**, with a clean handoff to Kubernetes.

---

## 2. Goals & non-goals

### Goals (v1)

- G1. A declarative, version-controllable **`AgentTeam` spec** (YAML) that fully describes a team of agents.
- G2. A **local runtime** that executes an `AgentTeam` on the developer's machine using the Claude Agent SDK as the per-agent execution engine.
- G3. **Harness adapters** so individual agents can be backed by Claude Code, Cursor, or Codex. (See the harness portability matrix in §4.7 — some harnesses are local-only.)
- G4. **Model/provider routing** so any agent can target Anthropic API, Bedrock, or Vertex AI, with per-agent overrides.
- G5. **Token-budget management** across providers, with visibility and enforcement.
- G6. **MCP server + skills + tools** declared once and shared across the team.
- G7. **Memory & persistence** local to the project, surviving restarts.
- G8. A **task layer + mailbox** for inter-agent coordination (durable, resumable).
- G9. **Attach & direct** — agents are interactive, steerable sessions; a human can attach to any running agent and direct it like a Claude Code/Cursor/Codex session. Optional guards pause-and-flag agents. First-class, on by default for supervised agents.
- G10. **Manifest export** that produces a KARO v2-compatible artifact (`karo export`).
- G11. **Validation** (`karo validate`) that catches spec errors before run/deploy.

### Non-goals (v1)

- N1. Building a new agent reasoning loop — reuse Claude Agent SDK.
- N2. Replicating Gas City's full feature set or organizational metaphors.
- N3. Running Kubernetes locally as the dev substrate (explicitly rejected — Kind-based local dev was the KARO v1 pain point; the local runtime is a plain OS process, not pods).
- N4. A GUI. CLI + config files only in v1.
- N5. Hosted/cloud execution (that is KARO v2 + the eventual managed offering).

---

## 3. Core concepts & glossary

| Term | Definition |
|---|---|
| **AgentTeam** | The top-level unit. A named, versioned collection of agents, shared resources, and a coordination policy. The portable artifact. |
| **Agent** | A single role within a team: its instructions (system prompt), a harness, a model binding, a tool/skill set, memory scope, and a mailbox address. |
| **Harness** | The execution front-end that drives an agent (`claude-code`, `cursor`, `codex`, or `sdk` for direct Claude Agent SDK). Cluster portability varies — see §4.7. |
| **Model binding** | The LLM backend an agent uses (`anthropic`, `bedrock`, `vertex`) + model id. Independent of harness where possible. |
| **Tool** | A callable capability exposed to an agent. Sourced from built-ins, custom in-process functions, or MCP servers. |
| **Skill** | A reusable instruction/capability module (Claude Code "skill" semantics) shared across agents. |
| **MCP server** | A Model Context Protocol server (stdio or http) providing tools to agents. |
| **Memory** | Durable state for an agent or team: conversation history, learned facts, artifacts. Local-file backed in CLI; pluggable. |
| **Task** | A unit of work with acceptance criteria, an owner, a state, and a result. Tasks form the durable, resumable work layer. |
| **TaskGraph** | The dependency/ordering relationship between tasks. Authored as `coordination.pipeline`/`coordination.edges` (§4.2); projected at runtime onto `Task.dependsOn`. |
| **Mailbox** | A durable message queue per agent enabling agent-to-agent and human-to-agent messaging. |
| **Attach** | Connecting a human to a running agent's live session to watch, inject direction, interrupt, or take over — the way you drive Claude Code / Cursor / Codex directly. |
| **Guard** | An optional rule that *pauses* an agent and flags it as needing attention (e.g. pause before `Bash`, pause on task complete) so a human knows when to attach. Not an approval workflow. |
| **Instructions** | An agent's operating instructions — its role, scope, and behavior (its system prompt). Authored as the **markdown body** of `agents/<name>/AGENT.md`; compiles to the `instructions` string in the compiled model. Same pattern as the body of a Claude Code / Cursor agent file. |
| **Project (folder)** | The local directory tree (§4.0) a developer authors: `karo.yaml` + `agents/` + `skills/` + `tools/` + `mcp/`. Compiled by the CLI into the `AgentTeam` model. |
| **Compiler** | The CLI component that turns a project folder (§4.0) into the canonical compiled `AgentTeam` (§4.2). |
| **Manifest** | The exported, KARO v2-ready (compiled) representation of an `AgentTeam` (Section 12). |

---

## 4. Authoring model & the `AgentTeam` contract

There are **two representations** of an `AgentTeam`, and keeping them distinct is central to the design:

1. **The local folder convention (§4.0)** — what humans *author*. Harness-native: markdown agent files with frontmatter (`AGENT.md`), skill directories (`SKILL.md`), tool modules, and a thin top-level `karo.yaml`. This is **not** a big Kubernetes-style document; it's convention-over-configuration, directly compatible with how Claude Code / Cursor / Codex already organize agents and skills.
2. **The compiled `AgentTeam` model (§4.2)** — what the system *operates on and interchanges* (the **`team.yaml`** form). The CLI **compiles** the folder into this canonical model for `validate`/`run`, and `karo export` emits it as the single KARO v2 CRD document. **This compiled model is identical in CLI and KARO v2** (KARO v2 only adds `runtime:`/`status:`). **Do not diverge it.**

> Mental model: the folder is the *source*; the compiled `AgentTeam` (`team.yaml`) is the *build artifact*. Humans edit the folder; machines exchange the compiled form. This is exactly why local and cluster stay in parity without anyone hand-editing verbose YAML — the pain point that sank KARO v1's Kind-based config.

### 4.0 The local folder convention (primary authoring surface)

```
my-team/                         # one KARO project = one (or more) AgentTeam(s)
  karo.yaml                      # THIN manifest: team name, defaults, coordination pattern,
                                 #   budgets, agent list (by ref), backend bindings, includes
  agents/
    planner/
      AGENT.md                   # frontmatter (name, harness, model, tool/mcp/skill refs, interaction)
                                 #   + instructions as the markdown BODY (the agent's system prompt)
    implementer/
      AGENT.md
    reviewer/
      AGENT.md
  skills/                        # Claude Code-style skill dirs, reused verbatim
    kubectl/
      SKILL.md
  tools/                         # custom in-process tools (python)
    jira.py
  mcp/
    servers.yaml                 # MCP server declarations (or one file per server)
  shared/                        # reusable fragments (resources, interaction) referenced via includes
  .karo/                         # local runtime state (memory/tasks/mail) — gitignored
```

**`karo.yaml` (thin top-level)** — only team-wide concerns and references, not agent bodies:

```yaml
apiVersion: karo.dev/v1
kind: AgentTeam
metadata:
  name: refactor-crew
  labels: { domain: platform }
spec:
  defaults:
    harness: sdk
    model: { provider: anthropic, id: claude-opus-4-8 }
  budgets: { team: { provider: anthropic, limit: 5000000, window: daily, onExceed: pause } }
  coordination: { pattern: lead-and-teammates, lead: planner }
  interaction: { attachable: true, autonomy: supervised }
  agents:                        # references; bodies live in agents/<name>/AGENT.md
    - { ref: agents/planner }
    - { ref: agents/implementer }
    - { ref: agents/reviewer }
  # resources are auto-discovered from skills/, tools/, mcp/ — declare overrides here only
```

> **Numeric literals — portability rule.** Write integers in plain decimal (`5000000`), never with
> underscore grouping (`5_000_000`). PyYAML (YAML 1.1) accepts `5_000_000` as an integer, but Go's
> `yaml.v3` (YAML 1.2 core schema) parses it as the **string** `"5_000_000"` — a silent CLI⇄operator
> divergence on the exact `int64` fields the parity invariant depends on. `karo validate` rejects
> underscores in numeric scalars (§7).

**`agents/<name>/AGENT.md`** — frontmatter carries the structured fields; the markdown body **is** the agent's instructions (system prompt):

```markdown
---
name: planner
harness: sdk
model: { provider: anthropic, id: claude-opus-4-8 }
tools: [jira_lookup]            # references into auto-discovered tools/ + mcp/ + skills/
mcp: [github]
interaction: { autonomy: supervised }
---
You are the lead. Decompose the incoming objective into tasks with explicit
acceptance criteria, assign them to teammates, and coordinate via the shared
task list. Do not write code yourself.
```

**Auto-discovery rules (convention over configuration):**

- Every dir under `agents/` containing an `AGENT.md` becomes an agent. The `agents:` list in `karo.yaml` is optional ordering/inclusion control; if omitted, all discovered agents are included.
- Every dir under `skills/` containing a `SKILL.md` is a skill, referenceable by dir name. (These are the *same* skill dirs harnesses use — drop existing ones in unchanged.)
- Every `*.py` under `tools/` exposing `@tool`-decorated functions registers those tools by function name.
- `mcp/servers.yaml` (or `mcp/*.yaml`) declares MCP servers, referenceable by `name`.
- `shared/` fragments are pulled in via `include:` in `karo.yaml`.

This means a developer can take existing `AGENTS.md`/`CLAUDE.md` content and skill directories and assemble a team by *placement*, not by writing a large spec.

#### 4.0.1 Folder → compiled-model mapping

| Folder source | Compiles into compiled `AgentTeam` field |
|---|---|
| `karo.yaml` `spec.defaults/budgets/coordination/interaction` | same fields (verbatim) |
| `agents/<n>/AGENT.md` frontmatter | an entry in `spec.agents[]` (structured fields) |
| `agents/<n>/AGENT.md` body | that agent's `instructions` string |
| `skills/<n>/` | `spec.resources.skills[]` (`source: ./skills/<n>`) |
| `tools/*.py` `@tool` fns | `spec.resources.tools[]` (`module: path:fn`) |
| `mcp/servers.yaml` entries | `spec.resources.mcpServers[]` |
| `shared/*` via `include:` | deep-merged before compile |

The compiler is deterministic and reversible enough that `karo export` of a folder and a hand-written compiled `team.yaml` with the same content produce equivalent output **after canonicalization** (see below).

##### Canonical form (the parity invariant)

"Byte-equivalent after canonicalization" is a CI-tested invariant (§19; KARO v2 §15), so the canonical
form is defined normatively in `karo-runtime` and is the single source of truth for both lanes.
Because two independent YAML emitters (Python on the CLI, Go in the operator round-trip) will **not**
produce identical bytes, the comparison is done over a **canonical JSON projection**, not raw YAML:

- keys sorted lexicographically at every level;
- integers rendered in plain decimal, no underscores, no exponent (see the numeric-literals rule);
- a single, explicit policy for defaults — *materialize* inherited defaults into the projection (so
  two specs that differ only in what they left implicit compare equal);
- block-scalar `instructions` normalized to a plain string (chomping/indentation removed);
- UTF-8, normalized booleans/nulls, single trailing newline.

Both `karo export` and the operator compare this canonical JSON; the raw on-disk YAML may differ in
incidental formatting without breaking parity.

### 4.1 The compiled form (interchange / CRD) — `team.yaml`

- The compiled `AgentTeam` is a single YAML/JSON document: `apiVersion: karo.dev/v1`, `kind: AgentTeam`, with the `spec` body in §4.2. On disk this is the **`team.yaml`** form.
- It is what `validate`, `run`, and `export` operate on, and what KARO v2 stores as a CRD.
- Supports `$ref`-style includes for shared fragments (§4.8) when authored directly; the folder model uses `include:` + auto-discovery instead.
- Hand-authoring the compiled form directly (single `team.yaml`, everything inline) remains **supported** for simple/one-off teams and for machine generation — the folder convention is the recommended default, not the only option.

### 4.2 Full annotated schema

```yaml
apiVersion: karo.dev/v1
kind: AgentTeam
metadata:
  name: refactor-crew                # DNS-1123 safe; unique within project
  labels:                            # free-form, used for filtering/observability
    domain: platform
    owner: platform                  # generic; do not ship personal identities in examples

spec:
  # ---- Team-level defaults (inherited by agents unless overridden) ----
  defaults:
    harness: sdk                     # sdk | claude-code | cursor | codex  (cluster support: §4.7)
    model:
      provider: anthropic            # anthropic | bedrock | vertex
      id: claude-opus-4-8            # provider-specific model id
      profile: anthropic-default     # optional: explicit credential profile (§14); local-only
      params:                        # optional generation params
        max_tokens: 8192
        temperature: 0.2
    permissionMode: prompt           # prompt | acceptEdits | plan | bypass  (tool-exec policy; §4.2.1)
    workingDir: ./workspace          # sandbox root for tool execution

  # ---- Token budgets (Section 8) ----
  budgets:
    team:
      provider: anthropic
      limit: 5000000                 # tokens; enforced across all agents (plain integer — no underscores)
      window: daily                  # daily | session | unbounded
      onExceed: pause                # warn | pause | hardstop
    perAgent: true                   # if true, each agent gets an equal share of the remainder
                                     #   after explicit agents[].budget overrides are subtracted

  # ---- Shared tools, skills, MCP servers (Section 9) ----
  resources:
    mcpServers:
      - name: github
        transport: stdio             # stdio | http
        command: ["gh-mcp-server"]   # for stdio
        env:
          GITHUB_TOKEN: ${env:GHE_TOKEN}
      - name: k8s
        transport: http
        url: https://mcp.internal/k8s
        headers:
          Authorization: ${secret:k8s-mcp-token}
    skills:                          # Claude Code skill dirs / packs
      - source: ./skills/kubectl
      - source: pack:example/python-development   # marketplace pack ref (pinned at export; §9/§12)
    tools:                           # custom in-process tools (CLI loads from a module)
      - name: jira_lookup
        module: ./tools/jira.py:lookup        # path:function
        description: "Fetch a Jira issue by key"
        schema:                      # JSON-schema-ish input
          key: { type: string }

  # ---- Memory (Section 10) ----
  memory:
    backend: file                    # LOCAL selection: file (CLI default) | sqlite | none
                                     #   on cluster, runtime.backends.memory OVERRIDES this (§10, v2 §6)
    path: ./.karo/memory
    scope: team                      # team | per-agent | both
    retention:
      maxItems: 2000
      gcStrategy: aggressive         # aggressive | lru | none

  # ---- Coordination policy (Section 11) ----
  coordination:
    pattern: lead-and-teammates      # lead-and-teammates | pipeline | swarm
    lead: planner                    # REQUIRED iff pattern == lead-and-teammates; names an agent
    pipeline:                        # REQUIRED iff pattern == pipeline; ignored otherwise
      stages: [planner, implementer, reviewer]   # deterministic order; must be acyclic, names resolve
    mailbox:
      backend: file                  # LOCAL: file (CLI) | redis | none  (cluster: runtime.backends)
      path: ./.karo/mail
      hardLimit: 500                 # max messages per mailbox; GC oldest beyond this
    taskLayer:
      backend: file                  # LOCAL: file (CLI) | sqlite       (cluster: runtime.backends)
      path: ./.karo/tasks
      resumable: true                # crashed/stopped tasks resume from last saved state

  # ---- Interaction: attach & direct (Section 13) ----
  # Agents are attachable, steerable sessions — not an approval workflow.
  # A human can `karo attach <agent>` at any time to watch, inject direction,
  # interrupt, or take over, exactly like driving Claude Code / Cursor / Codex directly.
  interaction:
    attachable: true                 # default true; every agent runs as an attachable session
    autonomy: supervised             # supervised | autonomous
                                     #   supervised = pause when a guard trips, wait for a human to attach
                                     #   autonomous = never pause for humans (CI / unattended)
    guards:                          # OPTIONAL: rules that pause an agent + flag "needs attention"
      - pauseBefore: [Bash]          # pause before running a matching tool, await attach
      - pauseOn: taskComplete        # pause when a task completes, await attach
    pauseTimeout: 0                  # seconds an agent waits paused; 0 = wait indefinitely

  # ---- The agents ----
  agents:
    - name: planner
      instructions: |              # authored as the body of agents/planner/AGENT.md
        You are the lead. Decompose the incoming objective into tasks with
        explicit acceptance criteria, assign them to teammates, and coordinate
        via the shared task list. Do not write code yourself.
      harness: sdk
      model: { provider: anthropic, id: claude-opus-4-8 }
      tools: [jira_lookup]           # subset of resources.tools/skills/mcp by name
      mcp: [github]
      memory: { scope: team }
      mailbox: planner               # address; defaults to agent name
      interaction: { autonomy: supervised }
      budget: { share: 0.5 }         # optional per-agent override (§8); fraction or { limit: <tokens> }

    - name: implementer
      instructions: |
        You implement assigned tasks to their acceptance criteria. You run code,
        edit files, and report results back to the planner's mailbox.
      harness: cursor                # local-only harness (§4.7); rejected by `karo export` for cluster
      model: { provider: anthropic, id: claude-sonnet-4-6 }
      mcp: [github, k8s]
      skills: [kubectl]
      permissionMode: acceptEdits

    - name: reviewer
      instructions: |
        You review completed work against acceptance criteria. You never edit;
        you return tasks with specific, actionable feedback.
      harness: sdk
      model: { provider: bedrock, id: anthropic.claude-sonnet-4-6-v1:0 }
      interaction: { autonomy: autonomous }   # reviewer runs unattended
```

#### 4.2.1 `permissionMode` vs `autonomy` vs `guards` (three distinct knobs)

These are frequently conflated; they are orthogonal:

- **`permissionMode`** (`prompt | acceptEdits | plan | bypass`) governs **tool-execution policy** —
  how the underlying harness treats tool calls (the SDK/Claude Code permission concept). It is *not*
  the team interaction model.
- **`interaction.autonomy`** (`supervised | autonomous`) governs whether the agent **pauses for a
  human** when a guard trips.
- **`interaction.guards`** are the **human-steering** pause-and-flag rules (§13).

Constraints:
- `permissionMode: prompt` with `autonomy: autonomous` is a **validation error** — an unattended agent
  cannot block on an interactive prompt.
- On cluster (headless pods, no TTY) `prompt` has no interactive surface. `karo export` either
  **coerces** `prompt` to an equivalent `pauseBefore`-style guard (so the agent pauses for attach
  rather than blocking on a dead TTY) or **rejects** it with guidance. The chosen behavior is recorded
  in the export report; `acceptEdits`/`plan`/`bypass` pass through unchanged.

### 4.3 Field-level rules

- `metadata.name` — required, DNS-1123, unique per project.
- `spec.agents` — at least one; names unique within team.
- Any agent field that exists in `spec.defaults` is **inherited unless overridden**.
- `tools`/`skills`/`mcp` on an agent are **references by name** into `spec.resources`. Referencing an undefined name is a validation error.
- `model.provider` + `model.id` (and optional `model.profile`) must resolve against a configured credential profile (Section 14) at run time, not validate time.
- `harness: cursor|codex|claude-code` requires the corresponding binary discoverable on `PATH` (validated by `karo doctor`, not `karo validate`), and is subject to the cluster-portability matrix (§4.7).
- Numeric scalars must be plain decimal (no `_` grouping) — see §4.0.

**Cross-field (conditional) validation rules** — enforced by `karo validate` *and* expressed in the shared JSON Schema (`if/then`) so the operator's CRD validation agrees:

- `coordination.lead` is **required iff** `pattern == lead-and-teammates`, and must name an existing agent; it is an error under `pipeline`/`swarm`.
- `coordination.pipeline.stages` is **required iff** `pattern == pipeline`; every entry must resolve to an agent and the implied graph must be acyclic.
- Every guard `pauseBefore: [<tool>]` name must resolve to a known built-in tool, a declared custom tool, or an MCP tool (`mcp:<server>/<tool>`); see the matcher grammar in §13.
- `pauseOn` ∈ `{ taskComplete, planReady, error }`.
- Budget math: the sum of explicit `agents[].budget` overrides must be ≤ the team `limit`; the remainder is split equally among the agents without an explicit override (§8).

### 4.4 Versioning

- Spec is `karo.dev/v1`. **The v1 schema is iterated internally** during the build-out — fields may be added/changed freely while it is internal-only — but it is **frozen as a stable contract before any external/public exposure** (open-source release or external users). After that freeze, changes follow normal Kubernetes API conventions (additive/optional changes within `v1`; breaking changes require a new version).
- The CLI and operator validate against the **same** generated JSON Schema (single source of truth in `karo-runtime`); CI fails on drift.
- **Accepted `apiVersion` set and forward-compat:** the only accepted value in this release is `karo.dev/v1`. The CLI/operator MUST reject any other `apiVersion` (e.g. a future `karo.dev/v2`) with a clear, actionable upgrade message naming the supported set — never silently coerce. The accepted-set string lives in `karo-runtime` and is mirrored in KARO v2 §3.
- Practical implication for the build: treat the schema as mutable now, but keep the generated JSON Schema authoritative so the internal-iteration phase can't silently diverge the CLI and operator.

### 4.5 Defaults & inheritance resolution order

For any agent field: **agent value → team `defaults` → built-in default**. Document the built-in defaults in a generated `karo schema --defaults` output. Credential resolution order for `model`: explicit `model.profile` → first profile matching `model.provider` → error (§14).

### 4.6 Secrets & interpolation

- `${env:VAR}` — read from process environment at run time.
- `${secret:NAME}` — read from the CLI's secret store (Section 14).
- `${file:path}` — inline file contents (e.g. long agent instructions).
- Interpolation happens at **run/export** time, never persisted in resolved form into committed files.

**Export transform to cluster (consumed by `karo export`, §12).** Because a pod's environment and
filesystem differ from a laptop's, each interpolation kind has a defined cluster mapping:

| Local form | `karo export` produces | Notes |
|---|---|---|
| `${secret:NAME}` | a `secretRef` (Secret name/key) | never the resolved value (§12 `--strip-secrets`) |
| `${env:VAR}` | a declared pod env var sourced from a Secret/ConfigMap, **or** export error with guidance | no implicit host-env leak into the cluster |
| `${file:path}` | the file contents **inlined** at export | yields a self-contained CRD (KARO v2 §3.1); the inline must be canonicalization-stable (§4.0.1) |

### 4.7 Harness/model independence — and the cluster portability matrix

Where a harness can accept an arbitrary model (e.g. SDK, Cursor with model selection), `model` applies. Where a harness pins its own model, `model` is advisory and the CLI must **warn** on mismatch rather than fail.

**Not every harness can run as a headless agent pod.** Cursor and Codex are interactive desktop/TUI
applications, not server-installable processes exposing a programmatic turn loop. The support matrix:

| Harness | Local run | Local attach | Cluster (KARO v2) |
|---|---|---|---|
| `sdk` | ✅ | streamed prompt | ✅ first-class (the agent-runtime image is the SDK) |
| `claude-code` | ✅ | native TUI (PTY/pane) | ⚠️ only if a headless mode is available; otherwise local-only |
| `cursor` | ✅ | native TUI (PTY/pane) | ❌ local-only |
| `codex` | ✅ | native TUI (PTY/pane) | ❌ local-only |

Implications:
- `sdk` is the only guaranteed cluster-portable harness in v1.
- `karo validate --target cluster` and `karo export` **reject** (or warn, per flag) an agent whose
  harness is not cluster-capable, so a `team.yaml` can't validate locally yet silently fail to deploy.
- The adapter advertises this via `HarnessCapabilities.clusterCapable` (§6.2); the Coordinator and
  exporter never special-case a harness beyond reading capabilities.

### 4.8 Includes / fragments

Support `include:` at top level to compose shared fragments (shared `resources`, shared `interaction`) so multiple teams in a repo reuse one MCP/skills definition:

```yaml
include:
  - ../shared/resources.yaml
  - ../shared/interaction.yaml
```

Merge semantics: included fragments are deep-merged in order; the local file wins on conflict.

---

## 5. Personas & primary use cases

- **P1 — The individual developer.** Wants to stop managing ad-hoc per-tool config; wants one project (`karo.yaml` folder, or a flat `team.yaml`) they can `karo run` and iterate on.
- **P2 — The teammate.** Clones the repo, runs `karo run`, gets the *same* agents/tools/skills without manually reconstructing anyone's setup.
- **P3 — The platform owner.** Wants the local definition to be the exact thing that later runs on Kubernetes via KARO v2.

Primary flows:

1. `karo init` → scaffold a project folder (`karo.yaml` + `agents/` + …), or `karo init --flat` for a single `team.yaml`.
2. Edit the project (in Claude Code/Cursor).
3. `karo validate` → fix errors.
4. `karo run --objective "..."` → team executes locally; attach to any agent to steer; guard-paused agents are flagged in the terminal.
5. `karo export -o karo-manifest.yaml` → hand to KARO v2.

---

## 6. Architecture

```
                +--------------------------------------------------+
                |                    karo CLI                      |
                |                                                  |
  project  -->  |  Compiler -> Loader/Resolver -> Validator ->     |
  folder /      |        |                          Planner        |
  team.yaml     |        v                            |           |
                |   Resource registry            Coordinator       |
                |   (MCP/skills/tools)          (tasks, mailbox,    |
                |        |                  attach/guards, memory) |
                |        v                              |          |
                |  Harness adapters  <------------------+          |
                |  [sdk][claude-code][cursor][codex]               |
                |        |                                          |
                |        v                                          |
                |  Model router (anthropic/bedrock/vertex)         |
                |        |                                          |
                |        v                                          |
                |  Token-budget meter + telemetry                  |
                +--------------------------------------------------+
                         |                         |
                  local .karo/ state         karo export
                  (memory, tasks, mail)      -> KARO v2 manifest
```

### 6.1 Component responsibilities

- **Compiler** — folder (§4.0) → compiled `AgentTeam` (§4.2): AGENT.md frontmatter+body, skills/tools/mcp auto-discovery, includes, mapping (§4.0.1), canonicalization.
- **Loader/Resolver** — read YAML, apply includes, resolve inheritance, interpolate secrets (lazily).
- **Validator** — schema + cross-reference + cross-field checks (Section 7 `validate`, §4.3).
- **Planner** — for the `lead-and-teammates` pattern, the lead agent decomposes the objective into tasks. For `pipeline`/`swarm`, planning is structural (defined by spec).
- **Coordinator** — owns the durable task layer, mailbox delivery, attach/guard gating (pausing agents and surfacing them for steering), memory reads/writes, and the authoritative budget gate. The heart of the runtime.
- **Harness adapters** — uniform interface (`run_turn`, `stream`, `interrupt`, `attach`) over each harness. The `sdk` adapter wraps Claude Agent SDK directly; others shell out / drive the respective tool.
- **Model router** — maps `model.provider/id`(`/profile`) to a concrete client; handles auth via credential profiles.
- **Token-budget meter** — counts tokens per provider/agent against an authoritative counter, enforces `budgets` (§8).
- **State store** — `.karo/` directory: `memory/`, `tasks/`, `mail/`. File-backed default; pluggable behind `karo-runtime` Protocols.

### 6.2 The harness adapter interface (the key abstraction)

```python
class HarnessAdapter(Protocol):
    name: str
    async def run_turn(self, ctx: AgentContext, message: Message) -> TurnResult: ...
    async def stream(self, ctx: AgentContext, message: Message) -> AsyncIterator[Event]: ...
    async def interrupt(self) -> None: ...
    async def attach(self, ctx: AgentContext) -> AttachSession: ...  # human takes the wheel:
                                                                     # live stream + inject turns +
                                                                     # interrupt + detach (hand back)
    def supports_model(self, model: ModelBinding) -> bool: ...
    def capabilities(self) -> HarnessCapabilities: ...   # tools? mcp? skills? streaming? attach?
                                                         #   clusterCapable? (gates export, §4.7)
```

`AgentContext` carries resolved tools, MCP handles, skills, memory accessor, mailbox accessor, budget meter, and an attach/guard gate (lets a human take over the session). **Adding a new harness = implementing this Protocol.** This is the extensibility seam that protects against the ecosystem shifting under you.

The adapter's `attach()` capability is what makes "direct an agent like Claude Code/Cursor/Codex" real: for the `sdk` adapter, attach streams the agent loop and accepts injected user turns; for `claude-code`/`cursor`/`codex` adapters, attach connects you to the harness's own interactive session (PTY/pane). On cluster only `clusterCapable` adapters run, and their attach is always a streamed SDK-style session — never a desktop PTY (§4.7, §13). See §13.

---

## 7. Command reference

All commands: `karo <command> [flags]`. Global flags: `--project/-p <dir>` (default cwd), `--verbose/-v`, `--json` (machine output), `--no-color`.

### `karo init`
Scaffold a new project **as the folder convention (§4.0)**.
- Flags: `--template <name>` (`minimal` | `lead-team` | `pipeline`), `--name <team>`, `--flat` (emit a single inline `team.yaml` instead of the folder, for one-off teams).
- Creates (folder mode, default): `karo.yaml`, `agents/<role>/AGENT.md` per template role, `skills/`, `tools/`, `mcp/servers.yaml`, `shared/`, `.karo/` (gitignored), `.gitignore`, `README.md`. Scaffolded templates contain **no** org-specific identifiers (CI-checked).

### `karo compile`
Compile the project folder (§4.0) into the canonical `AgentTeam` model (§4.2, the `team.yaml` form) and print it.
- Flags: `-o <file>`, `--format yaml|json`. Used for inspection/debugging and by `validate`/`run`/`export` internally. Deterministic, canonicalized output (§4.0.1).

### `karo validate`
Compile the folder, then statically validate. **No network, no model calls.**
- Flags: `--target local|cluster` (default `local`; `cluster` additionally enforces the harness portability matrix §4.7 and the `permissionMode` cluster rules §4.2.1).
- Checks: folder structure + frontmatter conformance; compiled-schema conformance; unique agent names; tool/skill/mcp references resolve against auto-discovered `skills/`/`tools/`/`mcp/`; cross-field rules (§4.3); budget math; guard matcher validity; numeric-literal lint (no `_`); include resolution; interpolation syntax.
- Exit non-zero on any error; `--json` emits structured diagnostics `{file,line,severity,code,message}` mapped back to the **source folder file** (e.g. `agents/planner/AGENT.md:3`), not the compiled form.

### `karo doctor`
Environment readiness.
- Checks: required harness binaries on PATH and versions; configured credential profiles reachable; MCP stdio commands launchable; clock/locale sanity.
- Reports per-check pass/warn/fail; never mutates anything.

### `karo run`
Execute a team locally.
- Flags: `--objective/-o "<text>"` (or `--objective-file`), `--project <dir>` (default cwd; the folder root) or `--team <file>` (a pre-compiled/flat `team.yaml`), `--resume <run-id>`, `--dry-run` (plan only, no model calls), `--max-turns <n>`, `--autonomy supervised|autonomous` (override; `autonomous` ignores guards for unattended/CI), `--attach <agent>` (start attached to an agent), `--detach` (background, return run-id).
- Behavior: **compiles folder** → validates → resolves credentials → starts Coordinator → drives agents until tasks reach terminal state or a budget/guard/limit halt. Streams events to terminal; guard-paused agents are flagged for attach; persists every state transition to `.karo/`.

### `karo attach`
Attach to a running agent and direct it — the core human interaction (§13).
- Usage: `karo attach <agent> [--run <id>]`.
- Behavior: connects you to the agent's live session. You see its stream and can: send a message / give direction (injected as a user turn), interrupt the current turn, edit/redirect, resume a guard-paused agent, or take over and later detach (`Ctrl-D`/`:detach`) to hand control back to the Coordinator.
- For `claude-code`/`cursor`/`codex` agents this drops you into that harness's native interactive session (PTY/pane); for `sdk` agents it's a streamed prompt. Same command, same feel as using those CLIs directly.
- Remote mode (`--context <ctx>`) attaches to an agent running under KARO v2 on a cluster (always a streamed session; §4.7).

### `karo ps`
List running agents and their state (`running` | `paused:guard` | `paused:budget` | `idle` | `attached`) so you know which agents want attention.

### `karo tasks`
Inspect/manipulate the task layer.
- Subcommands: `list [--run <id>] [--state ...]`, `show <task-id>`, `retry <task-id>`, `cancel <task-id>`, `assign <task-id> <agent>`.

### `karo mail`
Inspect mailboxes.
- Subcommands: `list <agent>`, `read <agent> <msg-id>`, `send <agent> --body "..."` (human → agent injection), `purge <agent>`.

### `karo memory`
Inspect/manage memory.
- Subcommands: `list [--scope team|agent --agent <n>]`, `get <key>`, `clear [--scope ...]`, `export <file>`, `import <file>`.

### `karo budget`
Token budget status.
- Subcommands: `status` (per-provider/per-agent usage vs limit), `reset` (new window).

### `karo export`
Compile the folder and produce the KARO v2 manifest (Section 12).
- Flags: `-o <file>` (default stdout), `--format yaml|json`, `--namespace <ns>` (sets `metadata.namespace`; required for namespaced cluster apply), `--profile <prod|staging>` (applies runtime defaults; §12), `--strip-secrets` (default true; emit `${secret:}`/`secretRef` refs, never resolved values), `--skills-bundle <oci|git|configmap>` (how to ship `skills/`+`tools/` to the cluster; default `oci`), `--push <ref>` (for `oci`).
- Behavior: compiles folder (§4.0) → canonical `AgentTeam` (instructions inlined into `spec.agents[].instructions`) → applies the interpolation export transform (§4.6) → rejects/coerces non-cluster harnesses (§4.7) and `permissionMode: prompt` (§4.2.1) → adds `runtime:` per profile → packages `skills/`+`tools/` per `--skills-bundle`, **resolving and pinning** `pack:` refs by version/digest, and rewrites `resources` refs to point at the bundle.
- Round-trip guarantee: the `spec` body in the export is equivalent (after canonicalization, §4.0.1) to the compiled local `spec`. `metadata.namespace` and the `runtime:` block live **outside** the shared `spec` body and are excluded from the parity comparison. **Parity invariant.**

### `karo schema`
Emit the JSON Schema for `AgentTeam`; `--defaults` lists built-in defaults; used by editors for validation.

### `karo version`
Version, build, spec apiVersion supported.

---

## 8. Token-budget management

- The meter wraps **every** model call (across all providers and harnesses) and records `(provider, agent, prompt_tokens, completion_tokens, ts)` to `.karo/usage.log`.
- For harnesses that do not return token counts (some CLI tools), estimate via a tokenizer and mark the record `estimated: true`.
- **Authoritative, synchronous enforcement.** Before a turn, the Coordinator calls the meter's `can_spend(agent, est)`, which checks-and-reserves against an **atomic counter** — a file-lock-guarded counter locally, a Redis `INCRBY`/check on cluster (KARO v2 §6). This is the *same* `karo-runtime` code on both sides, so the `onExceed` decision is identical local and remote. Observability metrics (§15; VM/Prometheus in v2) are derived from the same records but are **never** the enforcement source (they lag, which would overspend).
- **Per-agent budgets.** Explicit `agents[].budget` (`{ limit }` or `{ share }`) is reserved first; `budgets.perAgent: true` splits the *remaining* team limit equally among agents without an explicit override. `karo validate` checks the override sum ≤ team limit (§4.3).
- On `budgets.*.onExceed` (`warn | pause | hardstop`):
  - `warn` — log/emit a budget event + continue.
  - `pause` — pause the affected agent(s) and flag for attach with a "budget exceeded — continue / raise-limit / stop" prompt on attach.
  - `hardstop` — terminate the run cleanly, persist state for `--resume`.
- Windows: `daily` resets at 00:00 UTC; `session` resets per run; `unbounded` never resets. `karo budget reset` forces a new window.
- `karo budget status --json` powers dashboards and the export's documented expectations.

---

## 9. Tools, skills, and MCP

- **Built-in tools** (file read/write, bash, grep/glob, web fetch) come from the `sdk` harness where available; expose a consistent allow/deny list via `permissionMode` and per-agent `tools`.
- **Custom tools** are Python functions referenced as `path:function`, loaded as in-process MCP tools (Claude Agent SDK `@tool` / `create_sdk_mcp_server` pattern). The CLI generates the in-process MCP server automatically from `resources.tools`.
- **MCP servers** (stdio/http) are launched/connected by the resource registry and handed to each agent that references them. stdio servers are spawned as child processes with declared `env`; http servers connect with declared `headers`. Connections are pooled and reused across agents.
- **Skills** are Claude Code-style skill directories or marketplace pack refs (`pack:owner/name`). The CLI materializes referenced skills into the agent's skill path before a run. On `karo export`, `pack:` refs are **resolved and pinned by version/digest** into the chosen bundle so local and cluster get byte-identical skill content (§12). This is how agent skills / MCP packs become shareable: they live in `skills/` and `shared/` in the repo, referenced by name.

> Why `resources` is the single declared surface: skills/MCP feel fragmented today because they live per-machine. KARO makes `resources` the one declared surface in-repo, so cloning the repo + `karo run` reproduces the exact tool/skill/MCP environment.

---

## 10. Memory & persistence

- Default backend `file` writes JSON records under `.karo/memory/{team|agent-name}/`. The `backend` field selects the **local** store; on cluster `runtime.backends.memory` overrides it with the same `MemoryStore` Protocol (KARO v2 §6).
- Records: `{id, scope, key, value, tags, ts}`. Append-only log + compacted index.
- `scope: both` means an agent reads team memory and its own; **writes default to agent scope** unless the write explicitly targets team scope.
- GC per `retention`: `aggressive` keeps only `maxItems` most-recent/most-referenced; `lru`; `none`.
- Pluggable interface so KARO v2 can swap to a networked backend without spec change:

```python
class MemoryStore(Protocol):
    async def put(self, scope, key, value, tags=None): ...
    async def get(self, scope, key): ...
    async def query(self, scope, tags=None, limit=None): ...
    async def gc(self, policy): ...
```

---

## 11. Coordination patterns

Implement three in v1; the `pattern` field selects:

- **lead-and-teammates** — the `lead` agent decomposes the objective into tasks, assigns via mailbox, teammates execute and report; lead synthesizes. Mirrors Claude Code Agent Teams semantics but **portable**.
- **pipeline** — agents arranged in a fixed sequence; each consumes the prior's output. The order is **declared explicitly** in `coordination.pipeline.stages` (§4.2), which the Coordinator projects onto `Task.dependsOn` edges at runtime. The graph must be acyclic and every stage must resolve to an agent (§4.3).
- **swarm** — agents pull from a shared task queue; first-available picks up the next ready task.

**Task claiming (swarm and any parallel pull).** Claiming must be **atomic** so two agents never run the same task. The shared store contract test exercises this. Backends:
- Postgres (KARO v2): `UPDATE tasks SET state='assigned', owner=$agent WHERE id = (SELECT id FROM tasks WHERE state='pending' AND deps_met ORDER BY created FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING …`, plus a lease/heartbeat so a dead owner's task is reclaimed.
- File (CLI): equivalent claim via lockfile / atomic rename of the task record, with the same lease semantics.

All three patterns run on the same Coordinator primitives (tasks + mailbox + memory + attach/guards). The pattern only changes *who creates tasks and how they're claimed*.

### Task lifecycle
`pending → assigned → in-progress → (blocked) → review → done | failed | cancelled`, with `(paused)` reachable from `in-progress` whenever a guard trips or a human attaches (resumes back to `in-progress` on detach/continue). `review` means the task is with a reviewer agent; `paused` means it's awaiting human attach — they are distinct. Every transition is persisted; `resumable: true` lets `karo run --resume <id>` continue from the last persisted state (the durable-bead idea, kept simple).

---

## 12. KARO v2 manifest export

`karo export` emits a document that KARO v2 applies directly. It is the **shared `spec` body** plus a `runtime:` block of sane production defaults:

```yaml
apiVersion: karo.dev/v1
kind: AgentTeam
metadata:
  name: refactor-crew
  namespace: agents            # set via --namespace; outside the parity comparison
  labels: { ... }
spec:                          # <-- equivalent to local spec after canonicalization (§4.0.1)
  ...
runtime:                       # <-- added by export; consumed only by KARO v2
  scaleToZero: true
  idleTimeoutSeconds: 300
  maxConcurrentAgents: 10
  backends:                    # map abstract backends -> concrete cluster services (shape == CRD, v2 §3.1)
    memory:  { kind: redis,    secretRef: { name: karo-redis } }
    mailbox: { kind: redis,    secretRef: { name: karo-redis } }
    tasks:   { kind: postgres, secretRef: { name: karo-pg } }
  observability:
    metrics: victoriametrics
    tracing: { exporter: otel, endpoint: http://otel-collector:4317 }
  secrets:                     # references only; resolved by cluster
    GHE_TOKEN: { secretRef: { name: ghe, key: token } }
```

> **`runtime.backends` shape is canonical and shared.** Each backend entry is
> `{ kind, secretRef: { name, key? } }` — the *same* shape the operator's CRD expects (KARO v2 §3.1).
> Earlier drafts emitted a bare `ref: <service-name>`; that is removed so a `karo export` applies
> verbatim with `kubectl apply` and survives CRD validation.

The `--profile` flag selects the `runtime:` defaults:

| `runtime` field | `--profile staging` | `--profile prod` |
|---|---|---|
| `scaleToZero` | `true` | `true` |
| `idleTimeoutSeconds` | `120` | `300` |
| `maxConcurrentAgents` | `5` | `10` |
| `observability.tracing` | sampled | full |

The CLI must **never** emit resolved secret values. `--skills-bundle` defaults to `oci` (ConfigMaps cap at ~1 MiB; see §9 / KARO v2 §3.1).

---

## 13. Interaction model: attach & direct

The interaction model is **not** an approval workflow with checkpoints. It mirrors how developers already work with Claude Code / Cursor / Codex: an agent is a **live session you can attach to and steer**. The Coordinator drives agents autonomously, but a human can take the wheel of any agent at any moment. (See §4.2.1 for how this relates to `permissionMode`.)

**Core behaviors:**

- **Every agent runs as an attachable session** (`interaction.attachable: true`, default). `karo attach <agent>` connects you to it.
- On attach you can: watch the live stream; **send direction** (injected as a user turn into the agent's loop); **interrupt** the current turn; redirect; resume a paused agent; then **detach** to hand control back to the Coordinator. This is the `HarnessAdapter.attach()` capability (§6.2).
- **The session is the harness's own session.** For `claude-code`/`cursor`/`codex` agents (local), attach drops you into that tool's native interactive TUI (PTY/pane). For `sdk` agents, attach is a streamed prompt with the same inject/interrupt verbs. On cluster, attach is always streamed (§4.7).
- **tmux-friendly (local):** with tmux available, each agent gets its own pane; `karo attach` focuses it, so you can watch a whole team at once and jump between agents (the Gas Town-style experience), without inheriting any of Gas Town's opinions.

**Autonomy levels** (`interaction.autonomy`, per-team default, per-agent override):

- `supervised` (default) — the agent runs autonomously **but** pauses and flags itself when a **guard** trips, waiting for a human to attach.
- `autonomous` — never pauses for humans (reviewer-style agents, CI, unattended runs). `--autonomy autonomous` forces this globally.

**Guards** (`interaction.guards`, optional) are lightweight pause-and-flag rules — *not* approvals:

- `pauseBefore: [<tool>...]` — pause just before a matching tool call, await attach.
- `pauseOn: taskComplete | planReady | error` — pause at a lifecycle moment, await attach.
- A guard simply moves the agent to `paused:guard`; `karo ps` lists it; you `karo attach` to continue or redirect. `pauseTimeout` controls how long it waits (0 = forever).

**Guard matcher grammar.** A `pauseBefore` entry matches by tool name: an exact built-in or custom
tool name (e.g. `Bash`, `jira_lookup`), an optional glob (`Write*`), or an MCP tool as
`mcp:<server>/<tool>` (e.g. `mcp:github/create_issue`). A matcher that resolves to no known tool is a
validation error (§4.3). `pauseOn` accepts only the enum above.

**Parity:** the same `interaction`/`guards` config is honored by KARO v2, which exposes attach over its API (`karo attach --context <ctx>`) and surfaces guard-paused agents the same way. Behavior is spec-identical local and on cluster — only the transport differs (local PTY/pane vs streamed cluster attach). Note the scale-to-zero interaction: a guard-paused agent awaiting attach is **exempt from idle reclamation** so attach always has a target (KARO v2 §5.1, §7).

---

## 14. Configuration & credentials

- Project config: the folder (`karo.yaml` + `agents/` + …) or a flat `team.yaml`, plus `shared/`.
- User config: `~/.config/karo/config.yaml` — credential **profiles** and defaults.
- Credential profiles (never committed):
  ```yaml
  profiles:
    anthropic-default: { provider: anthropic, apiKeyEnv: ANTHROPIC_API_KEY }
    bedrock-eu:        { provider: bedrock, region: eu-west-1, awsProfile: my-aws-profile }
    vertex-prod:       { provider: vertex, project: my-gcp-project, location: europe-west1 }
  ```
- Secret store for `${secret:NAME}`: OS keychain where available, encrypted file fallback. `karo secret set/get/rm`.
- Resolution: explicit `model.profile: <name>` on the agent → first profile matching `model.provider` → error. `model.profile` is a **local** credential-selection mechanism; the operator ignores it and uses IRSA / Workload Identity / mounted Secrets instead (KARO v2 §8), so it does not affect the shared-`spec` parity comparison.

---

## 15. Observability (local)

- Structured event log (`.karo/runs/<run-id>/events.jsonl`) using the **canonical event vocabulary** below — the same vocabulary KARO v2 maps to metrics/traces (v2 §9), so dashboards match across local and cluster.

| Event type | Core fields (besides `ts`, `run`, `agent`) |
|---|---|
| `turn.start` / `turn.end` | `turn_id`, (`end`: `status`, `tokens`) |
| `tool.call` | `tool`, `args_digest`, `result_status` |
| `mailbox.send` / `mailbox.recv` | `from`, `to`, `msg_id` |
| `task.transition` | `task_id`, `from_state`, `to_state` |
| `attach` / `detach` | `user` |
| `guard.pause` | `guard`, `reason` (`pauseBefore:<tool>` \| `pauseOn:<event>` \| `budget`) |
| `human.inject` | `user`, `body_digest` |
| `model.usage` | `provider`, `prompt_tokens`, `completion_tokens`, `estimated` |
| `budget.halt` | `provider`, `mode` (`warn`\|`pause`\|`hardstop`), `used`, `limit` |

- `karo run` renders a live tree (agent → task → tool) in the terminal; `--json` streams the events above for tooling.
- Local OTel exporter optional (`KARO_OTEL_ENDPOINT`) so local traces match KARO v2 traces.

---

## 16. Tech stack (recommended)

- **Language:** Python 3.12+ (Claude Agent SDK is Python-native; in-process MCP tools are Python). Choose Python for v1 — it keeps SDK parity and is the language of the shared `karo-runtime`. (KARO v2's controller is Go for orchestration only; no agent logic lives in Go.)
- **CLI framework:** `typer` (or `click`).
- **Async runtime:** `asyncio` throughout (SDK is async).
- **Validation:** `pydantic` v2 models generated from / generating the JSON Schema.
- **State:** plain files (`json`/`jsonl`) + `sqlite` optional backend.
- **MCP:** Claude Agent SDK in-process server for custom tools; `mcp` client libs for external stdio/http.
- **Packaging:** `pipx`-installable; single entrypoint `karo`.

---

## 17. Project layout (to scaffold)

> Two distinct layouts: **(a)** this CLI's own source repo (below), and **(b)** the *user's authored team folder* — that is the §4.0 convention (`karo.yaml` + `agents/` + `skills/` + `tools/` + `mcp/`), produced by `karo init`. Don't confuse them.

**(a) The CLI source repo:**

```
karo/                              # this CLI repo
  pyproject.toml
  karo/
    __init__.py
    cli.py                         # typer app, command wiring
    spec/
      models.py                    # pydantic AgentTeam models (the compiled schema)
      compile.py                   # FOLDER (§4.0) -> compiled AgentTeam; AGENT.md frontmatter+body,
                                   #   skills/ tools/ mcp/ auto-discovery, includes, mapping (§4.0.1),
                                   #   canonicalization
      frontmatter.py               # AGENT.md / SKILL.md frontmatter parsing
      loader.py                    # includes, inheritance, interpolation (on compiled form)
      validate.py                  # validates compiled form (+ cross-field rules); maps diagnostics to source
      schema_export.py             # JSON Schema generation (source of truth, shared w/ operator)
    runtime/
      coordinator.py               # tasks + mailbox + attach/guards + memory orchestration
      planner.py
      patterns/                    # lead.py, pipeline.py, swarm.py
      budget.py                    # authoritative counter (file lock locally; Redis on cluster)
      events.py
    harness/
      base.py                      # HarnessAdapter Protocol (incl. clusterCapable capability)
      sdk_adapter.py               # Claude Agent SDK
      claude_code_adapter.py
      cursor_adapter.py
      codex_adapter.py
    models/
      router.py
      providers/                   # anthropic.py, bedrock.py, vertex.py
    resources/
      registry.py                  # mcp + skills + tools
      mcp.py
      skills.py
      tools.py
    state/
      memory.py
      tasks.py
      mailbox.py
      store_file.py
    config/
      profiles.py
      secrets.py
    exporter/
      manifest.py                  # karo export -> KARO v2 (namespace, runtime, secrets transform,
                                   #   harness gating, pack pinning, skills/tools bundling)
  templates/                       # init templates: minimal, lead-team, pipeline
                                   #   each template IS a §4.0 folder skeleton (no org identifiers)
  tests/
```

**(b) The user's authored team folder:** see §4.0 — that is what `karo init` emits and what developers edit and commit.

> Per the parallel-build plan (§18 / `PRD-KARO-v2.md` §14), `spec/` (models, compile, frontmatter, validate, schema), `runtime/`, `harness/`, `models/`, `resources/`, and the store **interfaces** are extracted into the shared `karo-runtime` library so the operator's agent pods import the identical code. The folder compiler and canonicalizer live there too, so `karo export` and the operator agree on the compiled form by construction.

---

## 18. Milestones — parallel build

**Parity (local == cluster) is the headline selling point, so the CLI and KARO v2 are built in parallel, both on top of the shared `karo-runtime` library.** They are not sequential. The two tracks share milestone numbers and converge at explicit **parity checkpoints** where the *same* `AgentTeam` is demonstrated running locally (CLI) and on Kubernetes (KARO v2). See `PRD-KARO-v2.md` §14 for the operator-side track and the canonical milestone table.

This document's track is the **CLI / local-runtime** lane:

- **M0 — Shared foundation (week 1, joint).** Stand up `karo-runtime` (the shared lib): the single source-of-truth JSON Schema for `AgentTeam` (including the cross-field `if/then` rules §4.3), pydantic models, loader (includes/inheritance/interpolation), validator, the canonicalizer (§4.0.1), and the **store Protocols** (`MemoryStore`, task store, mailbox) + `HarnessAdapter` Protocol. CLI side adds `karo init/validate/schema` against it. **This milestone is shared with KARO v2 M0** — both repos depend on the artifacts produced here. No divergence after this.
- **M1 — Single-agent run via SDK (week 2).** `sdk` harness adapter, model router (anthropic), token meter (authoritative counter), file-backed stores, `karo run` for a one-agent team; `karo attach` + a `pauseBefore` guard. *(Operator M1 builds the same single agent on-cluster against the same `karo-runtime`.)*
- **M2 — Coordination (weeks 3–4).** Coordinator: tasks + mailbox + lead-and-teammates; atomic task claim; `karo tasks/mail/memory`; resume. **★ Parity checkpoint A:** the same `team.yaml` runs locally here and on Kind via KARO v2 M2; deterministic-fixture parity test (§19) must pass across both.
- **M3 — Multi-harness + multi-provider (week 5).** Cursor + Codex adapters (local-only per §4.7); Bedrock + Vertex providers; per-agent overrides; budgets across providers. *(Shared adapters/router live in `karo-runtime`, so the operator gets these for free in its M3.)*
- **M4 — Export + polish (week 6).** `karo export` (round-trip guarantee, namespace, harness gating, pack pinning), `karo doctor`, pipeline/swarm patterns, `karo` remote mode (target a cluster), docs, `pipx` packaging. **★ Parity checkpoint B:** `karo export` → `kubectl apply` → identical task graph + attach/guard behavior on cluster; demo-ready.

Target: usable locally by M2 **and** the local→Kind handoff demonstrable at Parity Checkpoint A (same week), because that handoff *is* the pitch. Shareable + cluster-deployable by M4.

> **Why parallel, not sequential:** if the operator is built months after the CLI, the two drift and the "same definition everywhere" claim becomes aspirational. Building both against `karo-runtime` from M0, with a working parity demo at M2, makes parity a tested invariant instead of a marketing line — and gives a compelling demo (laptop run → `kubectl apply` → same agents in the cluster) much earlier.

---

## 19. Testing strategy

- **Schema/golden tests** for loader+validator (fixtures of good/bad `team.yaml`, including cross-field rule violations and underscore-numeric rejection).
- **Adapter contract tests**: a shared test suite every `HarnessAdapter` must pass (mocked model), including `clusterCapable` advertising.
- **Coordinator tests**: deterministic task-lifecycle and resume tests with a fake harness; **atomic-claim concurrency test** (no double execution under parallel pull).
- **Budget tests**: enforcement at each `onExceed` mode via the authoritative counter (no overspend under concurrency).
- **Canonicalization / export round-trip test**: `local spec == export.spec` after canonicalization, across a Python emit and a Go round-trip of the same fixture (catches the YAML-int and block-scalar drift).
- **Smoke e2e** (opt-in, requires creds): a 3-agent `lead-team` completes a trivial objective.

---

## 20. Open questions (decide during build)

1. Do Cursor/Codex adapters drive the tools' CLIs or their APIs? (Prefer CLI for parity with how devs actually use them; revisit if rate limits bite.) Both are local-only regardless (§4.7).
2. Token estimation tokenizer for harnesses that don't report usage — which library, and how to keep it provider-accurate.
3. Mailbox delivery semantics under `swarm` — at-least-once vs exactly-once locally (file backend). Start at-least-once, idempotent task handlers; task *claiming* is atomic regardless (§11).
4. Do we need a `karo watch` (re-run on file change) in v1, or defer? (Defer.)

---

## 21. Guardrails for the implementer

- **Keep the `spec` body identical to KARO v2.** When in doubt, change `runtime:`, never `spec:`.
- **The `HarnessAdapter` Protocol is sacred** — all harness differences hide behind it (including cluster-capability). No harness-specific logic leaks into Coordinator/Planner.
- **Never persist resolved secrets** into any file the user might commit or `karo export`.
- **Budget enforcement is authoritative and synchronous**, never derived from lagging metrics.
- **Local runtime is OS processes, not Kubernetes.** Do not introduce Kind/pods here.
- **No org-specific identifiers in shipped templates/examples** (CI-checked) — this is destined for OSS.
- Prefer **small, durable state transitions** over clever in-memory orchestration — resumability is a feature, not an afterthought.
