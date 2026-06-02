# 00 — Orientation & M0 Inventory

> Ground truth for the KARO M0 review. Every claim cites a path. Build/test
> output is in `00-build.log`. Reviewer hats: K8s operator engineer · agent-platform
> architect · skeptical adopter.

## 0.1 Repo shape & languages

`joe2far/karo` is a **monorepo matching the PRD v2 §13 layout** — this is the
PRD architecture, **not** the older Go/14-CRD "v0.4.0-alpha" design the prompt
warned about. There is no `agentgateway`, no MCP sidecar, no 14 CRDs. The
headline-drift scenario does **not** apply; the drift here is *within* the new
design (see §0.4).

| Component | Path | Language | Matches PRD |
|---|---|---|---|
| Shared runtime lib | `karo-runtime/karo_runtime/` | Python 3.11+ (pydantic v2, PyYAML) | ✅ CLI §17 / v2 §13 |
| CLI | `cli/karo/` | Python (typer) | ✅ CLI §17a |
| Operator | `operator/` | Go 1.24 (controller-runtime 0.17, kubebuilder) | ✅ v2 §13 |
| Agent image | `agent-runtime-image/` | Python (entrypoint) | ✅ v2 §13 |

Stack matches the PRD recommendation (Python `karo-runtime`+CLI, Go operator).
Python is 3.11 (`karo-runtime/pyproject.toml` `requires-python=">=3.11"`); PRD §16
recommends 3.12+ — minor, CI uses 3.12.

## 0.2 Toolchain reality check (see `00-build.log`)

| Check | Result |
|---|---|
| `pip install -e karo-runtime -e cli` | ✅ ok |
| `pytest karo-runtime` | ✅ **47 passed** |
| `pytest cli` | ✅ **9 passed** |
| `go build ./...` | ✅ ok |
| `go vet ./...` | ✅ ok |
| `go test ./...` | ✅ ok (controller unit tests only; no envtest) |
| `golangci-lint run` | ✅ **0 issues** |
| `ruff check` | ⚠️ **16 findings** (12 unused imports, 4 `E741` ambiguous `l`) — not a CI gate; no ruff config in repo |
| mypy | ⚠️ not project-configured; fails only on missing 3rd-party stubs |
| **CI schema drift-check** | ❌ **FAILS on a clean tree** — see Finding **B1** |
| `karo compile` (default yaml) | ❌ **crashes** — see Finding **B2** |
| `karo init/validate/export` | ✅ work end-to-end |

Cluster tooling (`kind`, `kubectl`, `helm`, `setup-envtest`) is **not installed**
in this environment; `docker` client present (daemon unverified). Operator e2e
(`operator/test/e2e/e2e_test.go:16`) is `//go:build e2e` + `t.Skip` — a placeholder.

### Finding #1 (toolchain) — two real failures
- **B1 — CI "Schema drift check" is broken.** `ci.yml:28` runs
  `diff <(tail -n +1 /tmp/schema.json) <(head -c -1 karo-runtime/schema/agentteam.schema.json)`.
  Committed and generated schemas are **byte-identical** (`}\n`), but `head -c -1`
  strips the committed file's trailing newline, so `diff` reports a spurious
  difference → non-zero exit → **the one CI gate enforcing schema parity is red
  even with zero drift.** Reproduced locally (`00-build.log`).
- **B2 — `karo compile` crashes in default (yaml) mode.**
  `RepresenterError: ('cannot represent an object', <Backend.file: 'file'>)` at
  `cli/karo/cli.py:121` — `yaml.safe_dump(doc)` is handed a doc containing
  pydantic enum objects. `karo compile --format json` works (uses
  `kr.canonical_json`). `compile` is an M0 deliverable command.

## 0.3 M0 deliverable inventory

State key: **T** = Implemented & tested · **U** = Implemented but unverified ·
**M** = Stubbed or missing · **P** = Partial/broken.

| # | M0 item (CLI §18 / v2 §14) | Path(s) | State | Evidence |
|---|---|---|---|---|
| 1 | Single source-of-truth **JSON Schema** | `karo_runtime/spec/schema_export.py:38`; `schema/agentteam.schema.json` | **P** | Generated *from* pydantic (`model_json_schema`), so pydantic is the real SoT; schema is a derived artifact. Drift-checked in CI but **gate is broken (B1)**; schema has **zero `if/then`** cross-field rules (§4.3 unmet) — `grep if/then/allOf/oneOf` → none. |
| 2 | **Pydantic models** for compiled spec (§4.2) | `karo_runtime/spec/models.py` | **T** | All §4.2 fields present (defaults/budgets/resources/memory/coordination/interaction/agents); `extra="forbid"` everywhere (`models.py:30`); tested `tests/test_spec.py`. No `charter`, no `hitl` (§0.4). |
| 3a | Folder **compiler** folder→compiled (§4.0/§4.0.1) | `karo_runtime/spec/compile.py`, `frontmatter.py` | **T** | Deterministic (sorted discovery `compile.py:129`); AGENT.md frontmatter **+ body→instructions** (`compile.py:159`); auto-discovers agents/skills/tools(AST `@tool`)/mcp; tested `test_spec.py:47-68`. |
| 3b | `include:` **deep-merge** local-wins (§4.8) | `karo_runtime/spec/loader.py:78-90` | **U** | Implemented (local merged last); **no test exercises `include:`**. |
| 3c | Defaults/**inheritance** order (§4.5) | `karo_runtime/spec/canonicalize.py:35-68` | **T** | agent→team→builtin for harness/model/permissionMode/interaction/mailbox; tested `test_spec.py:77`. `BUILTIN_DEFAULTS` is a hand-kept duplicate (`schema_export.py:15`) with no agreement test. |
| 3d | **Secret interpolation** `${env\|secret\|file}` lazy (§4.6) | `karo_runtime/spec/loader.py:24,97-126` | **U** | Lazy primitives; compiler never resolves → never persisted. **No spec-layer unit test**; only export-boundary test (`test_export.py:79`). |
| 4 | **Validator** + diagnostics → source **file:line** | `karo_runtime/spec/validate.py` | **P** | Cross-field rules implemented in Python (lead/pipeline/budget/prompt-autonomous/guard); maps to source **file** but **always `line=0`** (`validate.py:65`) — PRD's `AGENT.md:3` precision unmet. |
| 5a | **Store Protocols** (Memory/Task/Mailbox) | `karo_runtime/stores/base.py:98-122` | **T** | Clean `@runtime_checkable` Protocols, backend-agnostic. |
| 5b | Store **contract/conformance** test (backend-agnostic) | `karo_runtime/tests/test_stores.py` | **M** | Docstrings claim "both pass the same contract tests" (`stores/base.py:5`) but tests **hard-code FileTaskStore etc., not parametrized**. A redis/pg backend would share zero asserts. Atomic `claim()` tested (`test_stores.py:27`) but **never called by the Coordinator** (uses non-atomic `_next_runnable`). |
| 6 | **HarnessAdapter Protocol** incl `attach()` (§6.2) | `karo_runtime/harness/base.py:82-90` | **P** | Signature complete (`run_turn/stream/interrupt/attach/supports_model/capabilities`); `HarnessCapabilities.cluster_capable` present. **`attach()` is a stub** (`base.py:71` `NotImplementedError`); `AgentContext.memory/mailbox/budget` **never injected** by Coordinator (`coordinator.py:130`); no attach-gate field. (attach() impl is M1 scope.) **No adapter contract test.** |
| 7 | CLI **init / validate / schema / compile** | `cli/karo/cli.py` | **P** | init/validate/schema **T** (`test_cli.py`); **compile broken in yaml (B2)**. |
| 8a | Operator: kubebuilder scaffold | `operator/PROJECT`, `cmd/main.go` | **T** | Real scaffold; builds/vets/tests; `golangci-lint` clean. |
| 8b | Go `AgentTeam`/`AgentTask` types **generated from shared schema** | `operator/api/v1/*.go` | **M** | Types are **hand-written, not generated from the schema**, and **already drifted** (§0.4). `AgentTask` states incl `review`+`paused` ✅ (`agenttask_types.go:21`). |
| 8c | Helm installs CRDs + empty controller | `operator/charts/karo/` | **U** | CRDs+controller+RBAC install; **no Dispatcher/Redis/Postgres/OTel** despite §11 (`values.yaml:21` flags are dead config). |
| 8d | `kubectl apply` sample → status **Pending** | `operator/internal/controller/agentteam_controller.go:67` | **M** | Reconcile sets `Phase="Running"` immediately; **`Pending` is never assigned anywhere**. M0 acceptance bar unmet; untested (no envtest reconcile test). |
| 9 | **CI drift-check** JSON Schema ⇄ Go types ⇄ models | `.github/workflows/ci.yml` | **M** | pydantic⇄schema check exists **but is broken (B1)**; Go⇄CRD-yaml check exists; **nothing bridges Go ⇄ JSON-Schema** — the central invariant is unenforced. |
| + | **Canonicalizer** (§4.0.1) | `karo_runtime/spec/canonicalize.py` | **T** | keys sorted, plain-decimal ints, defaults materialized, trailing newline, namespace/runtime excluded; tested `test_spec.py:77-96`, `test_export.py:98-127`. Block-scalar `instructions` normalization unasserted; **no folder-compile == flat-team.yaml round-trip test**. |

## 0.4 Spec ↔ code delta (§4.2)

Compiled-spec field audit (pydantic `models.py` vs JSON Schema vs Go CRD):

- ✅ **No `charter` remnants** — field is `instructions` (`models.py:272`, `Agent.instructions` ← AGENT.md body). `grep charter` → none anywhere.
- ✅ **No `hitl`/checkpoint remnants** — interaction is `interaction:`+`guards:` (`attachable`, `autonomy: supervised|autonomous`, `pauseBefore`/`pauseOn`). `grep hitl|checkpoint` → none.
- ✅ **`apiVersion: karo.dev/v1` everywhere**, with an accepted-set allowlist on **both** lanes: `models.py:23 ACCEPTED_API_VERSIONS`, `groupversion_info.go:28 AcceptedAPIVersions`; other versions rejected (`validate.py`, `agentteam_controller.go:82`, tested `agentteam_controller_test.go:14`).
- ✅ **Task states include both `review` and `paused`, distinct** — `stores/base.py:23-24` (`paused = "..." # distinct from review`); Go `agenttask_types.go:21` enum lists all nine.

### ⚠️ Critical divergence — the Go CRD is a lossy subset of the shared spec
The Go `AgentTeamSpec` (`agentteam_types.go:146`) and generated CRD
(`config/crd/karo.dev_agentteams.yaml`) **omit fields that exist in the pydantic
model and JSON Schema**:

| Shared-spec field (pydantic/schema) | In Go CRD `spec`? |
|---|---|
| `resources` (mcpServers/skills/tools) | ❌ **absent** |
| `memory` | ❌ **absent** |
| `coordination.mailbox`, `coordination.taskLayer` | ❌ **absent** (only pattern/lead/pipeline) |
| `agents[].permissionMode`, `agents[].budget`, `agents[].memory` | ❌ **absent** |
| `model.params` | ❌ **absent** |

The code comment at `agentteam_types.go:7-11` claims a
`x-kubernetes-preserve-unknown-fields` escape hatch makes the full schema
"round-trip even where the Go type is coarse-grained." **This is false** —
`grep preserve-unknown` over the CRDs returns nothing; `spec` has no such marker.
Structural CRDs **prune** unknown fields, so `kubectl apply` of a real
`karo export` (which *does* emit `spec.resources`/`spec.memory`/mailbox/taskLayer)
would **silently strip them** → the exact CLI⇄cluster parity break M0 exists to
prevent. Also: schema's `AgentTeamSpec` has no `required`/`minItems` on agents
while the CRD requires `agents` `minItems:1` — a direct validation disagreement;
`Backend.kind` enums differ (Go `redis;postgres;sqlite;file` vs schema
`file;sqlite;redis;none`).

## 0.5 Bottom line for Phase 0

The foundation is **real and largely working on the Python side** (56 tests pass,
clean architecture, no charter/hitl, correct apiVersion/task-states), but the
**single invariant the whole product rests on — "the same `AgentTeam` runs
locally and on Kubernetes" — is not enforced and is already violated**:
1. The schema↔Go bridge does not exist; the Go CRD is a lossy subset that prunes
   `resources`/`memory` on apply (§0.4).
2. The one Python-side drift gate (CI) is broken (B1).
3. Cross-field rules live only in Python; the operator is the weaker validator.
Plus two functional defects: `karo compile` yaml crashes (B2); operator never
reaches the M0-specified `Pending` status.

Detailed verdicts in `01-foundation.md`; fixes in `02-fixplan.md`.
