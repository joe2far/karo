<p align="center">
  <img src="docs/logo.svg" alt="KARO — Kubernetes Agent Runtime Orchestrator" width="320">
</p>

<h1 align="center">KARO — Kubernetes Agent Runtime Orchestrator</h1>

> **Define your agent team once, locally, in portable config. Run it on your
> laptop today. Push the exact same definition to Kubernetes tomorrow — no
> rewrite.**

KARO is a portable, harness-agnostic, model-agnostic way to define a **team of
agents** — their roles, tools, memory, task flow, and where a human can attach
and steer — that runs locally for fast iteration and graduates *unchanged* to a
production Kubernetes runtime.

The whole product rests on one invariant: **the `spec` body of an `AgentTeam` is
byte-identical (after canonicalization) on the laptop and on the cluster.** That
parity is a tested CI invariant, not a marketing line.

---

## This is a monorepo

Four top-level components share one `AgentTeam` contract:

| Path | What it is | Language |
|---|---|---|
| [`karo-runtime/`](karo-runtime/) | **Shared library** — spec models, folder compiler, canonicalizer, validator, JSON Schema, store Protocols, harness adapters, model router, Coordinator, budget meter, exporter. Imported by *both* lanes. | Python |
| [`cli/`](cli/) | The **`karo` CLI** — author, validate, run locally, and `karo export` to Kubernetes. | Python |
| [`operator/`](operator/) | The **KARO v2 operator** — reconciles `AgentTeam`/`AgentTask` CRDs into durable, scale-to-zero agent workloads. | Go (controller-runtime) |
| [`agent-runtime-image/`](agent-runtime-image/) | The pod image that runs **one** agent via `karo-runtime` (the SDK adapter). | Python |

`karo-runtime` is the single source of truth. The CLI uses its **file** stores;
the operator supplies **Redis/Postgres** stores implementing the same Protocols.
Everything above the store layer (compiler, validator, adapters, router,
coordinator, exporter) is shared code — that is *why* local and cluster can't
drift.

**New here?** Start with the [**usage guide**](docs/USAGE.md) (install → author →
run → sling at one agent → human gate → export) and the runnable
[**`examples/`**](examples/) (a Jira-integrated team you can run offline).

The full design lives in [`docs/`](docs/):
[`USAGE.md`](docs/USAGE.md) (the hands-on guide),
[`PRD-KARO-CLI.md`](docs/PRD-KARO-CLI.md) (the CLI / local runtime),
[`PRD-KARO-v2.md`](docs/PRD-KARO-v2.md) (the operator), and
[`SPEC-REVIEW.md`](docs/SPEC-REVIEW.md) (the pre-build review that shaped them).

---

## Quickstart (local)

```bash
# Install the CLI — no clone, no build step (pipx if present, else pip):
curl -fsSL https://raw.githubusercontent.com/joe2far/karo/main/scripts/install.sh | bash
# (or, hacking on KARO itself, editable from the monorepo: pip install -e karo-runtime -e cli)

# Scaffold a team as the folder convention (karo.yaml + agents/ + skills/ + …)
karo init --name refactor-team --template lead-team

# Static checks — no network, no model calls
karo validate

# Run it locally (omit creds and it runs in a deterministic stub mode)
karo run -o "tidy up the logging module"

# Sling a prompt straight at one agent (skip the lead's decomposition)
karo sling reviewer "review the auth changes on JIRA-789"
# …or at an agent in another team folder, or on a cluster (team = namespace):
karo sling pm-team/deploy-approver "approve JIRA-789" --context kind-karo

# Inspect the durable state
karo ps
karo tasks list
karo memory list

# Hand off to Kubernetes — same spec, plus a runtime: block
karo export -o team-manifest.yaml --namespace agents
```

### The authoring model

You author a **folder** (the source); the CLI compiles it into the canonical
`AgentTeam` (the build artifact):

```
refactor-crew/
  karo.yaml            # thin: team name, defaults, coordination, budgets, agent refs
  agents/
    planner/AGENT.md   # frontmatter (harness, model, refs) + the body IS the system prompt
    implementer/AGENT.md
    reviewer/AGENT.md
  skills/              # Claude Code-style skill dirs, reused verbatim
  tools/               # custom in-process @tool functions (auto-discovered)
  mcp/servers.yaml     # MCP server declarations
  # karo.yaml also declares resources.repos (git repos agents work on, cloned on run)
  shared/              # reusable fragments pulled in via include:
  .karo/               # local runtime state (memory/tasks/mail) — gitignored
```

`karo init --flat` emits a single inline `team.yaml` instead, for one-off teams.

---

## Command reference (CLI)

| Command | Purpose |
|---|---|
| `karo init` | Scaffold a project (`--template minimal\|lead-team\|pipeline`, `--flat`). |
| `karo compile` | Folder → canonical `AgentTeam` (deterministic, canonicalized). |
| `karo validate` | Static + cross-field validation (`--target local\|cluster`, `--json`). |
| `karo doctor` | Environment readiness (harness binaries, profiles, SDK). |
| `karo run` | Run a team locally, or sling at `[team/]agent` (`run [team/]agent "msg"`, `-o`, `--agent`, `--context`, `--dry-run`, `--resume`). |
| `karo sling` | Fire one objective at one agent: `karo sling team/agent "msg"` (local folder, or `--context` namespace). |
| `karo ps` | List agents and their state. |
| `karo tasks` | `list\|show\|retry\|cancel\|assign` the durable task layer. |
| `karo mail` | `list\|read\|send\|purge` agent mailboxes. |
| `karo memory` | `list\|get\|clear` team/agent memory. |
| `karo budget` | `status\|reset` the authoritative token budget. |
| `karo attach` | Attach to a running agent and steer it (local PTY/streamed; `--context` for cluster). |
| `karo export` | Compile + produce the KARO v2 manifest (`--namespace`, `--profile`, `--strip-secrets`). |
| `karo schema` | Emit the JSON Schema (`--defaults` for built-in defaults). |
| `karo secret` | `set\|get\|rm` local secrets for `${secret:}` interpolation. |
| `karo version` | Version + supported spec apiVersion. |

See `docs/PRD-KARO-CLI.md` §7 for the complete flag-level reference.

---

## Deploy (Kubernetes)

```bash
cd operator

# Build + generate CRDs/DeepCopy/RBAC
make build manifests

# Install CRDs and the controller via Helm
helm upgrade --install karo charts/karo -n karo-system --create-namespace

# Apply an exported team (kubectl-native; ArgoCD/Flux-friendly)
kubectl apply -f config/samples/karo_v1_agentteam.yaml
kubectl get agentteams
```

The controller is **orchestration only** (provisioning, scaling, status, budget
policy). All agent-reasoning logic lives in the Python `karo-runtime` running in
the agent pods — the same code the CLI runs locally. The agent image is built
with build context at the repo root:

```bash
docker build -f agent-runtime-image/Dockerfile -t ghcr.io/karo/agent-runtime:v2 .
```

---

## Concepts

- **AgentTeam** — the portable artifact: agents + shared resources + coordination policy.
- **Harness** — the execution front-end (`sdk`, `claude-code`, `cursor`, `codex`). Only `sdk` is cluster-capable; the others are local-only (portability matrix, CLI §4.7).
- **Coordination patterns** — `lead-and-teammates`, `pipeline`, `swarm`, all on the same Coordinator primitives (durable tasks + mailbox + memory + attach/guards).
- **Attach & direct** — every agent is a live, steerable session, not an approval queue. `karo attach` to watch, inject a turn, interrupt, or take over. **Guards** pause-and-flag an agent for attention; they are not approvals.
- **Working repos** — agents declare the git repos they work on (`resources.repos` + per-agent `repos:`); KARO clones them into the workspace on run and sets each agent's working dir. Auth is the runner's own git config, so the team carries no credentials.
- **Budgets** — authoritative, synchronous token accounting (atomic counter), identical locally and on cluster.
- **Parity** — `karo export`'s spec body equals the local spec after canonicalization; this is tested against a fixture with a large integer and a block-scalar to catch YAML-portability drift.

---

## Development

```bash
# Python: shared runtime + CLI
pip install -e karo-runtime -e cli pytest pytest-asyncio
(cd karo-runtime && pytest)      # spec, stores, budget, coordinator, export
(cd cli && pytest)               # end-to-end CLI

# Go: the operator
cd operator
make generate manifests          # regenerate DeepCopy + CRDs/RBAC
go test ./...
```

CI enforces the headline invariants: the committed JSON Schema matches the
generator, the generated Go types/CRDs are up to date, and scaffolded templates
carry no org-specific identifiers.

### Status & what's outstanding

Done and tested: **M0** (shared foundation), **M1** (single-agent SDK run, file
stores, budget meter, attach/`pauseBefore` guard), **M2** (Coordinator: durable
tasks + mailbox + lead-and-teammates, atomic claim, resume, `karo tasks/mail/
memory`), and a working slice of **M3/M4**: direct dispatch + `karo sling
[team/]agent "msg"` (local folders *and* cluster namespaces via `--context`),
per-provider budgets, the provider-agnostic model router, **git working repos**
(`resources.repos` + per-agent `repos:`, cloned into the workspace on run),
prompt-from-file (`-f`/`@file`), and an explicit `coordination.reviewer` field.
**Parity Checkpoint A passes** in CI across file *and* real Postgres; the operator
builds/vets/tests green (envtest) with the same spec.

Outstanding for a complete solution:

- **M3 — runtime depth:** real Bedrock/Vertex API integration in the SDK adapter
  (router + budgets already provider-agnostic); **model-driven** decomposition
  (today's lead decomposition is deterministic — one task per teammate);
  **mailbox-driven** coordination (inboxes are recorded, not yet consumed to
  drive dialogue); full PTY attach for the local-only Cursor/Codex harnesses.
- **Operator (cluster) M3 — scale-from-zero + claim loop:** *done and envtest-tested.*
  The `AgentTeam` reconciler watches `AgentTask` projections and sets per-agent
  replicas — a task (e.g. from `karo sling … --context`) wakes its owner **and**
  the lead; a guard-paused task keeps its agent up; terminal work scales back to
  zero. The agent pod now runs a long-lived **claim loop** (`Coordinator.serve`)
  instead of single-shot, so a teammate that starts before the lead plans still
  picks up its task. *Remaining connective tissue:* syncing the `AgentTask`
  projection with the authoritative Postgres tasks store so the reconciler reacts
  to real run state (today the projection is the signal), plus mailbox-stream
  routing.
- **M4 — polish:** `karo export` round-trip hardening, remote `karo attach
  --context` *streaming* against a live cluster, OTel/metrics + gVisor + EKS/GKE
  validation, and **published pipx/PyPI packaging** (install is no-build today,
  but not yet on a public index).

See the milestone tables in `docs/PRD-KARO-CLI.md` §18 and `docs/PRD-KARO-v2.md`
§14, and the adversarial M2 review in [`review/07-m2-verdict.md`](review/07-m2-verdict.md).

## License

Apache-2.0. See [`LICENSE`](LICENSE).
