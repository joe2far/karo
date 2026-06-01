# KARO v2 — Kubernetes Agent-Team Operator — PRD & Implementation Spec

> **Status:** Draft v1 (for build)
> **Audience:** implementers, platform engineers (review)
> **Companion doc:** `PRD-KARO-CLI.md` (produces the `AgentTeam` manifest this operator runs)
> **Review:** see `SPEC-REVIEW.md` for the findings that shaped this revision.

---

## 0. One-sentence definition

> **KARO v2 is a Kubernetes operator that takes an `AgentTeam` definition — the exact same `spec` you ran locally with the KARO CLI — and runs it as a durable, observable, scale-to-zero workload on Kubernetes, across any LLM backend (Anthropic, Bedrock, Vertex), independent of which harness authored the agents.**

This is the defensible, scarce piece. Local team-definition tooling is increasingly table stakes; **reliably running agent teams at production scale on Kubernetes — with durable tasks, mailboxes, live attach/steer, observability, scale-to-zero, and provider flexibility — is the gap nobody owns.**

---

## 1. Problem statement

Teams prototype agent workflows locally (KARO CLI, Claude Code Agent Teams, Gas City) but cannot run them **perpetually and reliably in production**:

- Laptops can't host always-on, long-looping agents.
- Claude "Managed Agents" run the loop on Anthropic's infra (good, but it's their cloud, single-vendor, and not on-prem/own-cluster).
- Existing K8s agent projects (e.g. `kagent`, `Agent Sandbox`) solve pieces (running an agent in a pod, warm sandboxes) but not the **portable team definition → durable multi-agent runtime** path with provider-agnostic backends and live human attach/steer.

KARO v2 is the **own-your-cluster production runtime** for the same `AgentTeam` artifact developers already use locally.

---

## 2. Goals & non-goals

### Goals (v1)

- G1. CRD-based representation of `AgentTeam` (shared `spec` from `PRD-KARO-CLI.md` §4) plus a `runtime:` block.
- G2. A controller that reconciles `AgentTeam` → running agent workloads + coordination services.
- G3. **Durable task layer, mailbox, and memory** backed by cluster services (Postgres/Redis), surviving pod restarts.
- G4. **Scale-to-zero / OnDemand** agents — provision on work arrival, reclaim when idle.
- G5. **Harness-agnostic execution within the cluster-capable set** — agents run via the same adapter model as the CLI; the SDK adapter runs in-process in the agent pod and is the first-class cluster harness. Interactive-only harnesses (`cursor`, `codex`, and `claude-code` without a headless mode) are **local-only** and rejected at export (`PRD-KARO-CLI.md` §4.7).
- G6. **Provider-agnostic model routing** (Anthropic, Bedrock, Vertex) with per-agent overrides; **no LLM hosting** by KARO — it calls provider endpoints.
- G7. **Attach & direct over the API** — a human can attach to any running agent in the cluster (`karo attach --context`) to watch, inject direction, interrupt, or take over, honoring the spec's `interaction`/`guards`. Guard-paused agents are surfaced for attention.
- G8. **Observability**: metrics (VictoriaMetrics/Prometheus), traces (OpenTelemetry), structured logs — parity with the local event vocabulary (`PRD-KARO-CLI.md` §15).
- G9. **Governance**: per-team token budgets, RBAC, network isolation, audit log.
- G10. Clean install via Helm; runs on EKS and GKE (and Kind for CI).

### Non-goals (v1)

- N1. Hosting LLMs. KARO calls Anthropic/Bedrock/Vertex; it does not run model inference.
- N2. Being the **local dev experience** — that is the KARO CLI (`PRD-KARO-CLI.md`). KARO is one product spanning both: developers author and iterate locally with the KARO CLI, and this document specifies the **KARO Kubernetes runtime** that the *same* `AgentTeam` graduates to. This PRD does not re-solve local authoring; it is the production/cluster half of KARO.
- N3. The eventual managed SaaS ("push to our cloud") — v1 is self-hosted operator only. (Managed offering is the later business layer.)
- N4. Replacing service meshes, GitOps, or CI — KARO integrates with them (ArgoCD/Flux-friendly CRDs), it doesn't reimplement them.

---

## 2.1 Relationship to KARO v1

KARO v1 (`joe2far/karo-v1`, archived) validated the core ideas as a Go operator built around **15 CRDs** in four layers. v2's central design move is to **collapse that surface into one self-contained `AgentTeam` CRD** (plus a managed `AgentTask`, plus a deferred `AgentChannel`), authored locally and exported as a single document. This is *why* the CRD count drops from 15 to ~2: humans no longer wire resources together with cross-CRD references; they author a folder and `karo export` produces one resolved object.

| KARO v1 CRD | Where it lands in v2 |
|---|---|
| `ModelConfig` | `spec.agents[].model` + credential profiles (CLI §14) |
| `MemoryStore` | `spec.memory` + `runtime.backends.memory` |
| `ToolSet` | `spec.resources.{tools,skills,mcpServers}` |
| `SandboxClass` | `runtime.scheduling` / pod `runtimeClassName` (gVisor/Kata; §10) |
| `AgentPolicy` | `spec.budgets` + RBAC/NetworkPolicy (§9) |
| `EvalSuite` | **dropped** — replaced by `reviewer` agents in the `lead-and-teammates` pattern (declarative output-quality gates are not a v1 feature; revisit later if needed) |
| `AgentSpec` | an entry in `spec.agents[]` |
| `AgentMailbox` | `spec.coordination.mailbox` → Redis streams (§6) |
| `AgentInstance` | an agent **pod** provisioned by the controller |
| `TaskGraph` | `spec.coordination.pipeline`/edges → `AgentTask.dependsOn` (§3.2) |
| `Dispatcher` | the Dispatcher Deployment (§4.1) — orchestration, not a CRD |
| `AgentTeam` | the primary CRD (now self-contained) |
| `TeamBinding` | **obsolete** — the team is self-contained, no name-mapping CRD needed |
| `AgentLoop` | **deferred** — continuous/scheduled execution is out of v1 scope (CLI defers `karo watch`) |
| `AgentChannel` | **deferred to v1.1** (§3.3) |

A second key shift: v1 ran agent reasoning in Go; **v2 moves all agent-reasoning logic into the Python `karo-runtime`** (Claude Agent SDK), leaving the Go controller for orchestration only (§4.2, §12). And v1's Kind-based local dev — the pain point — is replaced by the CLI's OS-process runtime.

---

## 3. Custom resources (CRDs)

Group `karo.dev`, version `v1`. The only accepted `apiVersion` is `karo.dev/v1`; the controller rejects any other value with a clear upgrade message (mirrors `PRD-KARO-CLI.md` §4.4).

### 3.1 `AgentTeam` (primary)

`spec` is **the shared, compiled body** from `PRD-KARO-CLI.md` §4.2 (agents, resources, memory, coordination, interaction, budgets). KARO v2 adds `runtime:` and a `status:`.

> **Authoring vs CRD.** Humans do **not** hand-write this CRD. They author the **folder convention** (`PRD-KARO-CLI.md` §4.0: `karo.yaml` + `agents/<n>/AGENT.md` + `skills/` + `tools/` + `mcp/`) and run `karo export`, which **compiles** the folder into this CRD: agent `instructions` are inlined from each `AGENT.md` body, `${file:}` interpolations are inlined, `${secret:}`/`${env:}` become `secretRef`s (CLI §4.6), and `skills/`+`tools/` are packaged into a bundle (OCI by default, or git/ConfigMap) with `pack:` refs pinned by digest and `spec.resources` rewritten to point at it. The operator therefore receives a fully-resolved, self-contained `AgentTeam`. GitOps applies it like any other CRD (ArgoCD/Flux). The compiler is the shared `karo-runtime` code, so what `karo export` emits and what the operator expects agree by construction.

```yaml
apiVersion: karo.dev/v1
kind: AgentTeam
metadata:
  name: refactor-crew
  namespace: agents             # set by `karo export --namespace`; teams are namespace-isolated (§9)
spec:
  # ===== identical to local CLI spec (do not diverge) =====
  defaults: { ... }
  budgets: { ... }
  resources: { mcpServers: [...], skills: [...], tools: [...] }
  memory: { ... }
  coordination: { pattern: lead-and-teammates, ... }   # pipeline uses spec.coordination.pipeline.stages
  interaction: { attachable: true, autonomy: supervised, guards: [...] }
  agents: [ ... ]

  # ===== KARO v2 runtime block (consumed only here) =====
  runtime:
    scaleToZero: true
    idleTimeoutSeconds: 300
    maxConcurrentAgents: 10
    scheduling:
      nodeSelector: { workload: agents }
      runtimeClassName: gvisor     # SandboxClass equivalent (§10)
      tolerations: [ ... ]
      resources:                   # default per-agent pod resources
        requests: { cpu: "500m", memory: "1Gi" }
        limits:   { cpu: "2",    memory: "4Gi" }
    backends:                      # shape is shared with `karo export` (CLI §12): { kind, secretRef }
      memory:  { kind: redis,    secretRef: { name: karo-redis } }
      mailbox: { kind: redis,    secretRef: { name: karo-redis } }
      tasks:   { kind: postgres, secretRef: { name: karo-pg } }
    observability:
      metrics: victoriametrics
      tracing: { exporter: otel, endpoint: http://otel-collector:4317 }
    image:
      agentRuntime: ghcr.io/karo/agent-runtime:v2
      pullPolicy: IfNotPresent

status:
  phase: Running                  # Pending | Provisioning | Running | Idle | Degraded | Failed
  activeAgents: 3
  pendingTasks: 2
  budget: { provider: anthropic, used: 1234567, limit: 5000000, window: daily }
  conditions: [ ... ]             # standard k8s conditions
  observedGeneration: 7
```

> **`runtime.backends` shape.** Each backend entry is `{ kind, secretRef: { name, key? } }` and is
> **byte-for-byte the same shape `karo export` emits** (CLI §12). Earlier drafts used a bare
> `ref: <name>` on the export side; that is removed so an export applies verbatim.

### 3.2 `AgentTask` (managed, usually not authored by users)

Represents one durable task. The controller creates these from coordination; persisted source of truth is the tasks backend, with `AgentTask` as the K8s-visible projection for `kubectl`/tooling. `dependsOn` edges are the runtime projection of `spec.coordination.pipeline.stages`/edges for the pipeline pattern (CLI §11).

```yaml
kind: AgentTask
spec:
  team: refactor-crew
  owner: implementer
  objective: "..."
  acceptanceCriteria: ["..."]
  dependsOn: [task-id, ...]
  guard: { pauseOn: taskComplete }   # optional: pause the owning agent here, await attach
status:
  state: in-progress              # pending|assigned|in-progress|blocked|review|paused|done|failed|cancelled
                                  #   review = with a reviewer agent; paused = guard/human attach pending
  attempts: 1
  owner: implementer              # claimant; set atomically on claim (§6)
  lease: 2026-05-31T...           # claim lease; reclaimed if the owner pod dies (§6)
  attachedBy: null                # set to the user when a human is attached/steering
  result: { ... }
  lastTransition: 2026-05-31T...
```

### 3.3 `AgentChannel` (optional, v1.1)

Declares a named, durable message channel between agents/teams. **Note:** in KARO v1 `AgentChannel` named *external* integrations (Slack/Telegram/Discord); v2 repurposes the name for **cross-team agent messaging** (intra-team messaging is `spec.coordination.mailbox`). This is a new meaning, not a carry-over. Defer to v1.1 unless cross-team messaging is needed in v1.

---

## 4. Architecture

```
                         Kubernetes cluster
  +------------------------------------------------------------------+
  |  karo-controller (Deployment, leader-elected)                    |
  |    - watches AgentTeam / AgentTask                               |
  |    - reconciles -> provisions runtime + services                |
  |    - owns scale-to-zero decisions, budget enforcement           |
  +---------------------------+--------------------------------------+
                              | creates / scales
        +---------------------+----------------------+
        v                     v                      v
  +-----------+        +--------------+        +--------------+
  | Dispatcher|        | Agent pods    |       | Coordination |
  | (Deploy)  |<------>| (per agent;   |<----->| backends     |
  | event bus |        |  scale 0..N)  |       |  Redis (mem, |
  | task pump |        |  runs harness |       |  mailbox)    |
  +-----------+        |  adapter +    |       |  Postgres    |
                       |  model router |       |  (tasks)     |
                       +-------+-------+       +--------------+
                               |
                               v
                   LLM providers (egress):
                   Anthropic API | Bedrock | Vertex AI
                               |
                       MCP servers (in-cluster or via MCP tunnel)
```

### 4.1 Components

- **karo-controller** — the operator. controller-runtime based. Reconciles `AgentTeam`/`AgentTask`. Decides provisioning, scaling, budget halts. Leader-elected for HA.
- **Dispatcher** — event-driven decoupling layer (carry-over from KARO v1's event-based Dispatcher/TaskGraph decoupling). Pumps ready tasks to agents, routes mailbox messages, emits lifecycle events. Stateless; backed by the coordination stores.
- **Agent pod** — runs the **agent-runtime** image: the same harness-adapter + model-router code as the CLI (shared library), executing **one** agent via the SDK adapter in-process. Scales 0..N. On `scaleToZero`, no pod exists until a task targets that agent (except paused-for-attach agents; §5.1). Only cluster-capable harnesses run here (G5; CLI §4.7).

  **Bootstrap contract.** A pod is parameterized entirely from env + the CRD; the entrypoint
  (`agent-runtime-image/entrypoint.py`) reads:
  - `KARO_TEAM`, `KARO_AGENT` — which agent of which `AgentTeam` this pod is;
  - `KARO_RUN_ID` — the run this pod participates in;
  - backend DSNs (`KARO_TASKS_DSN`, `KARO_MEMORY_DSN`, `KARO_MAILBOX_DSN`) injected from the
    `runtime.backends[*].secretRef`;
  - provider credentials via IRSA/Workload Identity/mounted Secret (§8);
  - `KARO_OTEL_ENDPOINT` from `runtime.observability.tracing`.

  It then loads the agent's slice of the shared spec from the CRD (a projected ConfigMap or the
  K8s API) and runs the `karo-runtime` Coordinator loop for that single agent. The attach transport
  (§7) targets this pod by `team/agent`.
- **Coordination backends** — Redis (memory, mailbox), Postgres (tasks). These make the local file backends' interfaces (`MemoryStore`, task store, mailbox) production-grade. **Same interfaces, different impl** — so the shared runtime library is backend-pluggable.
- **Model router / providers** — identical to CLI; egress to provider endpoints. Credentials via mounted secrets / IRSA (EKS) / Workload Identity (GKE).
- **MCP** — in-cluster MCP servers as Services, or external via an MCP-tunnel gateway (single outbound connection; no inbound exposure) for private-network tools.

### 4.2 Shared runtime library (critical)

The harness adapters, model router, coordinator logic, canonicalizer, and store **interfaces** are a **single shared library** used by both the CLI (file backends, OS process) and KARO (network backends, pods). This guarantees behavioral parity and is the engineering expression of "runs the same locally and in production." Package it (`karo-runtime`) and depend on it from both the CLI and the agent image. (In the monorepo layout of §13, `karo-runtime` is a top-level package the other components import.)

---

## 5. Reconciliation logic (controller)

For each `AgentTeam`:

1. **Validate** the `spec` (reuse CLI validator from `karo-runtime`, including the cross-field rules and the accepted-`apiVersion` check). Validating-webhook rejects non-cluster-capable harnesses if one slipped past export.
2. **Ensure backends**: verify Redis/Postgres reachable per `runtime.backends`; create schemas/keyspaces if missing.
3. **Materialize resources**: deploy/connect MCP servers; mount skills (init-container pulls the pinned skill/tool bundle into a shared volume).
4. **Provision agents**:
   - If `scaleToZero: false` → ensure one pod per agent.
   - If `scaleToZero: true` → ensure zero pods; register agents with Dispatcher so pods are created on first targeted task.
5. **Wire coordination**: ensure Dispatcher knows the team's pattern (lead/pipeline/swarm), mailbox topology, task queue.
6. **Enforce budget** (see §5.6).
7. **Update status** every reconcile; set conditions (`Ready`, `BudgetOK`, `BackendsReady`).

### 5.1 Scale-to-zero / OnDemand

- Idle detection: an agent with no in-flight turn and an empty mailbox for `idleTimeoutSeconds` is scaled to zero.
- **Paused-for-attach exemption.** An agent in `state: paused` (guard-tripped, awaiting human attach — possibly with `pauseTimeout: 0` = forever) is **not** idle and is **exempt from scale-to-zero**. Otherwise it would be reclaimed and `karo attach` would have no pod to reach — a deadlock. (If a deployment must reclaim such pods, the alternative is to persist paused state and **re-provision the pod on attach** from committed state, since all state is durable; the exemption is the default, re-provision is the documented cold-start fallback.) `idleTimeoutSeconds` therefore applies only to non-paused idle agents and must be read together with `pauseTimeout`.
- Cold-start mitigation: optionally maintain a **warm pool** of generic agent-runtime pods (à la Agent Sandbox `SandboxWarmPool`) that get specialized on claim, eliminating per-invocation cold start. Config: `runtime.warmPool: { size: N }` (v1.1 if time-constrained; document the seam).
- A task arriving for a zero-scaled agent triggers the Dispatcher to request a pod from the controller; task waits in `pending` until the pod is `Ready`.

### 5.6 Budget enforcement (authoritative)

Budgets are enforced **synchronously and atomically**, not via lagging metrics. Each agent pod, before every turn, calls `karo-runtime`'s `can_spend(agent, est)` which does a check-and-reserve against an **atomic counter in Redis** (`INCRBY` with a limit check) — the same code path the CLI runs against a file-lock counter (CLI §8). This makes the `onExceed` decision identical local and on-cluster and prevents N parallel pods from overshooting before a metrics scrape notices.

The controller subscribes to budget events for *status and policy actions*, not for the spend decision: on a crossed `budgets.*.limit` it applies `onExceed` — `warn` = event + continue; `pause` = stop dispatching new turns and surface affected agents; `hardstop` = scale all agents to zero and mark `status.phase=Degraded`. `status.budget` reflects the authoritative counter. (Metrics/VM in §9 are observability only.)

---

## 6. Durable coordination (the core)

- **Tasks** (Postgres): authoritative task records + dependency edges (the runtime projection of `spec.coordination.pipeline`). Every state transition is a committed row update → survives any pod/controller restart. `AgentTask` CRDs are projections updated by the controller for visibility.
  - **Atomic claim.** Agents claim work with `UPDATE tasks SET state='assigned', owner=$pod, lease=now()+ttl WHERE id = (SELECT id FROM tasks WHERE state='pending' AND deps_met ORDER BY created FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING …`. A lease/heartbeat reclaims a task whose owner pod died. This is what makes `swarm` (and any parallel pull) safe — two pods never run the same task. The file backend (CLI) implements the identical claim semantics via lockfile/atomic rename, and both pass the shared store contract test.
- **Mailbox** (Redis streams): per-agent durable streams; `hardLimit` enforced via stream trimming (aggressive GC, carry-over from KARO v1 AgentMailbox semantics). At-least-once delivery; handlers idempotent.
- **Memory** (Redis): `MemoryStore` interface impl; team and per-agent scopes; retention/GC per spec.
- **Resumability**: because state lives in the backends, killing every pod and reconciling again resumes all in-flight tasks from their last committed state. This is the production version of the CLI's `--resume`.

---

## 7. Attach & direct (human interaction)

Same model as the CLI (`PRD-KARO-CLI.md` §13), exposed over the cluster API: an agent is a **live session a human can attach to and steer**, not an approval queue.

- **Attach over the API.** `karo attach --context <ctx> <team>/<agent>` opens a streamed, bidirectional session to a running agent pod: watch the live stream, inject direction (a user turn), interrupt the current turn, take over, then detach to hand control back to the Dispatcher. Transport is a streamed attach to the agent pod (websocket/SPDY, `kubectl attach`-style); the agent-runtime exposes the `HarnessAdapter.attach()` capability over it. On cluster the attach session is always streamed (never a desktop PTY), since only cluster-capable harnesses run here (CLI §4.7).
- **Guards** from `spec.interaction.guards` are honored identically. When a guard trips (e.g. `pauseBefore: [Bash]`, `pauseOn: taskComplete`), the owning agent moves to `state: paused` and the controller emits a **guard event**. `kubectl get agenttasks`/`agentteams` shows paused agents; `karo ps --context` lists what needs attention. Resuming = attaching and continuing (or redirecting). A paused agent is exempt from scale-to-zero so attach always reaches it (§5.1).
- **`autonomy: autonomous`** agents never pause for humans (unattended/CI). `pauseTimeout` controls how long a paused agent waits before applying its configured default (continue|fail|escalate). Note `permissionMode: prompt` is invalid headless and is coerced/rejected at export (CLI §4.2.1), so a pod never blocks on a dead TTY.
- Minimal web UI for browsing/attaching is v1.1; v1 is API + `karo` remote mode.

---

## 8. Model routing & providers

- Same router as CLI. Per-agent `model.provider/id`. The local-only `model.profile` (CLI §14) is ignored on cluster. Credentials:
  - **EKS**: IRSA / Pod Identity for Bedrock; Anthropic/Vertex keys via mounted Secrets.
  - **GKE**: Workload Identity for Vertex; keys via Secrets/Secret Manager CSI.
- **Cost routing** is explicit (per-agent model choice), not magic — reviewer on cheap Sonnet/Bedrock, lead on Opus, etc.
- Egress: document required NetworkPolicy allow-lists for provider endpoints + MCP.

---

## 9. Observability & governance

- **Metrics** (VictoriaMetrics/Prometheus): per-team/agent token usage, turns, task throughput, queue depth, guard-paused / attach-pending count, cold-start latency, agent pod count. The local event vocabulary (`PRD-KARO-CLI.md` §15) maps 1:1 to metrics for parity. Metrics are **observability only** — budget enforcement is the authoritative counter in §5.6, never a metrics scrape.
- **Tracing** (OTel): a trace per task; spans for turns, tool calls, mailbox hops, model calls. Endpoint from `runtime.observability.tracing`.
- **Logs**: structured JSON, same event vocabulary as CLI `events.jsonl`.
- **Audit**: every attach session (who steered which agent, injected turns), guard pause, budget halt, and spec change recorded immutably.
- **RBAC**: namespace-scoped; teams isolated by namespace (set via `karo export --namespace`); controller least-privilege.
- **Budgets**: enforced centrally and authoritatively per §5.6; `status.budget` reflects live usage.

---

## 10. Security

- Agent pods run untrusted-ish model-directed code → **strong isolation**: gVisor/Kata sandboxed `runtimeClassName` (the `runtime.scheduling.runtimeClassName`, v1's `SandboxClass` equivalent) where available; seccomp; read-only rootfs; no host mounts; per-agent ServiceAccount.
- Secrets never baked into images or `AgentTeam` YAML — only `secretRef`s; resolved at pod start. The CLI's export transform (CLI §4.6) guarantees `${secret:}`/`${env:}` become `secretRef`s before the CRD ever leaves a laptop.
- **MCP tunnel** option for private tools: a lightweight in-cluster gateway makes a single outbound connection; agents reach private MCP servers with no inbound exposure.
- NetworkPolicies: agents may egress only to declared provider endpoints + MCP + coordination backends.

---

## 11. Install & integration

- **Helm chart** installs: CRDs, controller, Dispatcher, default Redis/Postgres (or BYO via `runtime.backends.secretRef`), OTel wiring, RBAC.
- **GitOps-native**: `AgentTeam` is a plain CRD → manage via ArgoCD/Flux. Reconciliation is declarative and idempotent. Because `karo export` produces a fully-resolved, namespace-stamped object, `kubectl apply`/ArgoCD needs no post-edit.
- **Platforms**: EKS + GKE first-class; Kind for CI/e2e.
- **`karo` remote mode**: the CLI can target a cluster (`karo --context <ctx> run/apply/tasks/ps/attach`) so the same CLI is the local *and* remote control surface.

---

## 12. Tech stack (recommended)

- **Operator:** Go + `controller-runtime` (kubebuilder scaffold). Standard, performant, idiomatic for CRDs.
- **Agent-runtime image:** Python 3.12 + the shared `karo-runtime` library (same code as CLI). Go controller + Python runtime is fine — they communicate via CRDs/backends, not in-process.
- **Backends:** Redis (mem/mailbox), Postgres (tasks). Pluggable.
- **Observability:** OTel SDK, Prometheus/VictoriaMetrics exposition.
- **Packaging:** Helm; images to GHCR; multi-arch (amd64/arm64).

> Note on the two-language split: the **behavioral logic** (adapters, router, coordinator, stores, canonicalizer) lives in the Python `karo-runtime` shared lib so CLI and agent pods are identical. The Go controller is *orchestration only* (provisioning, scaling, status) and holds **no** agent-reasoning logic. Keep that boundary clean.

---

## 13. Repo layout (to scaffold)

> **This repository (`joe2far/karo`) is a monorepo** containing the operator, the CLI, the shared
> runtime, and the agent image — not three separate repos. The earlier drafts described independent
> `karo` / `karo-operator` / `karo-runtime` repos; in practice they are top-level modules here so the
> shared `karo-runtime` is imported by both the CLI and the agent image without cross-repo
> versioning friction. The four top-level components:

```
karo/                              # this monorepo
  cli/                             # the KARO CLI (PRD-KARO-CLI.md §17a) — `karo` entrypoint, pipx-installable
    karo/ ...                      # cli.py, exporter/, config/  (imports karo_runtime)
    pyproject.toml

  karo-runtime/                    # SHARED Python lib (imported by CLI + agent image)
    karo_runtime/
      spec/                        # models, compile, frontmatter, validate, schema_export, canonicalize
      harness/ models/ runtime/ resources/   # adapters, router, coordinator, registry
      stores/
        file.py                    # used by CLI
        redis.py                   # used by KARO
        postgres.py                # used by KARO
    pyproject.toml

  operator/                        # Go operator (kubebuilder)
    Makefile
    PROJECT
    api/v1/
      agentteam_types.go           # CRD types; spec mirrors the shared JSON Schema
      agenttask_types.go
      zz_generated.deepcopy.go
    internal/controller/
      agentteam_controller.go
      agenttask_controller.go
      provisioner.go               # pods, services, scaling
      backends.go                  # ensure redis/postgres
      budget.go                    # status/policy actions (authority lives in karo-runtime)
      scale.go                     # scale-to-zero / warm pool (paused-agent exemption, §5.1)
    internal/dispatcher/
      dispatcher.go                # task pump, mailbox routing, events
    config/
      crd/ rbac/ manager/ samples/
    charts/karo/                   # Helm chart
    test/e2e/                      # Kind-based e2e

  agent-runtime-image/             # Dockerfile + entrypoint that runs ONE agent
    Dockerfile
    entrypoint.py                  # reads bootstrap env/CRD (§4.1), runs via karo_runtime
```

> CI generates/validates the Go `AgentTeam` types against the **same** JSON Schema `karo-runtime`
> exports, failing on drift (§16). Because everything is one repo, a schema change lands atomically
> across the CLI, the runtime, and the Go types in a single commit.

---

## 14. Milestones — parallel build (canonical table)

**The CLI and KARO v2 are built in parallel, not sequentially.** Parity — "the same `AgentTeam` runs locally and on Kubernetes" — is the product's headline selling point, so it is made a *tested invariant from the start* rather than something bolted on after the CLI ships. Both lanes depend on the shared **`karo-runtime`** library (see §4.2 and §13) and converge at explicit **parity checkpoints**.

### 14.1 The two lanes

| Wk | Shared foundation | CLI / local lane | KARO v2 / cluster lane |
|----|-------------------|------------------|------------------------|
| 1 | **M0 (joint):** `karo-runtime` — single source-of-truth JSON Schema for `AgentTeam` (incl. cross-field `if/then` rules + accepted-apiVersion); pydantic models + loader + validator + **canonicalizer**; store **Protocols** (`MemoryStore`, tasks, mailbox) + `HarnessAdapter` Protocol (incl. `clusterCapable`). CI drift-check (Go types ⇄ schema ⇄ pydantic). | `karo init/validate/schema` on the shared lib. | kubebuilder scaffold; `AgentTeam`/`AgentTask` Go types generated from the shared schema; Helm installs CRDs + empty controller; `kubectl apply` sample → status `Pending`. |
| 2–3 | adapters/router land in `karo-runtime` | **CLI-M1:** `sdk` adapter, anthropic router, token meter (authoritative counter), **file** stores, one-agent `karo run`; `karo attach` + a guard. | **v2-M1:** provision one agent pod from `agent-runtime` image (bootstrap contract §4.1); **Redis/Postgres** stores (same Protocols); one-agent objective end-to-end; status reflects task state. |
| 4–5 | coordinator logic in `karo-runtime` | **CLI-M2:** Coordinator — tasks + mailbox + lead-and-teammates; atomic claim; `karo tasks/mail/memory`; resume. | **v2-M2:** Dispatcher; lead-and-teammates on cluster; durable tasks/mailbox/memory; atomic claim; **kill-all-pods → reconcile → resume**; authoritative budget enforcement. |
| | | **★ PARITY CHECKPOINT A** — same `team.yaml` runs locally (CLI-M2) and on Kind (v2-M2); the deterministic-fixture **parity test** (§15) passes across both. **This is the first demoable version of the core pitch.** |||
| 6 | providers in `karo-runtime` | **CLI-M3:** Cursor + Codex adapters (local-only); Bedrock + Vertex; per-agent overrides; cross-provider budgets. | **v2-M3:** scale-to-zero / on-demand provisioning (paused-agent exemption); Bedrock + Vertex via IRSA/Workload Identity; per-agent cost routing; NetworkPolicies. |
| 7–8 | — | **CLI-M4:** `karo export` (round-trip, namespace, harness gating, pack pinning), `karo doctor`, pipeline/swarm, **`karo` remote mode**, docs, `pipx`. | **v2-M4:** OTel traces, VM metrics, audit; **attach API + `karo attach` remote** + guard surfacing; gVisor isolation; EKS+GKE validation; docs. |
| | | **★ PARITY CHECKPOINT B** — `karo export` → `kubectl apply` → identical task graph + attach/guard behavior on cluster. Full local→production handoff demo-ready. |||
| v1.1 | — | watch mode, richer templates | warm pool, `AgentChannel`, minimal attach/observability UI, MCP tunnel |

### 14.2 Rules for parallel execution

- **M0 is genuinely joint and blocking.** Neither lane proceeds past M0 until `karo-runtime` exposes the schema, Protocols, canonicalizer, and adapter interface. This ~1 week of shared work is what guarantees the lanes can't drift.
- **Backends are the only per-lane difference at M1–M2.** CLI uses `stores/file.py`; KARO uses `stores/redis.py` + `stores/postgres.py` — both implementing the *same* `karo-runtime` Protocols and passing the *same* contract tests (including atomic-claim). Everything above the store layer (adapters, router, coordinator, planner) is shared code.
- **Parity checkpoints are gates, not afterthoughts.** A lane may not advance to its M3 until Parity Checkpoint A passes. The parity test (§15) lives in CI and runs both lanes against one fixture.
- **One person can still drive both lanes** by working M0 first, then alternating: the shared lib means cluster work is mostly provisioning/scaling (Go controller) on top of already-proven Python logic, so the marginal cost of the cluster lane after M0 is lower than it looks.

> **Why this matters commercially:** building the operator in parallel means the "laptop run → `kubectl apply` → same agents in the cluster" demo exists at week 5 (Parity Checkpoint A), not month 3. That demo *is* the differentiator versus single-vendor managed offerings and UI-locked Agent Teams.

---

## 15. Testing strategy

- **Controller unit tests** with `envtest` (fake API server): reconcile → expected objects/status.
- **Store conformance**: Redis/Postgres impls pass the **same** `karo-runtime` store contract tests the file backend passes — including the **atomic-claim concurrency** test (no double execution).
- **e2e on Kind**: apply a 3-agent team, drive an objective, kill all pods mid-run, assert resume to completion.
- **Scale-to-zero tests**: idle → 0 pods; task → pod provisioned → completes; **paused agent is NOT scaled to zero** and remains attachable.
- **Budget tests**: crossing limit triggers pause/hardstop and correct status; **no overspend under N parallel pods** (authoritative counter).
- **Parity test**: same `AgentTeam` produces an equivalent **canonical-JSON spec** and equivalent task graph + outputs locally (CLI) and on Kind (KARO) for a deterministic fixture; the test round-trips a fixture with a large integer and a block-scalar `instructions` field through both the Python and Go emitters to catch YAML-portability drift.

---

## 16. Risks & mitigations

- **A vendor ships a competing K8s runtime.** Mitigation: KARO's edge is *provider-agnostic, own-cluster, portable-from-local*. Managed Agents is single-vendor cloud; KARO is BYO-cluster + BYO-provider. Keep the shared-spec portability story tight.
- **Two-language complexity (Go + Python).** Mitigation: hard boundary — Go does orchestration only; all agent logic in the shared Python lib. No reasoning logic in Go.
- **Cold-start latency hurts UX.** Mitigation: warm pool (v1.1), generous `idleTimeoutSeconds` defaults.
- **Cost runaway from looping agents.** Mitigation: authoritative atomic budget counter with hardstop (§5.6); per-task max attempts; observability on token burn.
- **Spec drift between CLI and operator.** Mitigation: a **single source-of-truth JSON Schema** in `karo-runtime` (in this monorepo); both the Go types and the Python models are generated/validated against it in CI; the canonical-form parity test guards the wire format.

---

## 17. Guardrails for the implementer

- **The `spec` body is the shared contract.** Generate/validate Go `AgentTeam` types against the same JSON Schema `karo-runtime` exports. CI must fail on drift. `runtime.backends` uses `{ kind, secretRef }` — the same shape `karo export` emits.
- **No agent-reasoning logic in the Go controller.** Provisioning, scaling, status, budget gating only; budget *authority* lives in `karo-runtime`.
- **All durable state lives in backends**, never only in pod memory — resumability is the headline feature.
- **Stores are pluggable behind `karo-runtime` Protocols.** Redis/Postgres impls must pass the identical contract tests as the file backend, including atomic claim.
- **Never bake secrets into images or CRDs** — `secretRef` + IRSA/Workload Identity only.
- **Scale-to-zero is default-on** in `runtime`; ensure correctness (no lost tasks, no reclaimed paused-for-attach agents) before optimizing cold start.
- **Build EKS + GKE clean**; use Kind only for CI.
