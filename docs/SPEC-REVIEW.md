# KARO v2 — Spec Review & Refinement Log

> **Scope:** a pre-implementation review of the two draft PRDs (`PRD-KARO-CLI.md`,
> `PRD-KARO-v2.md`) before the `karo.dev/v1` schema freezes (CLI §4.4). The refinements were
> applied **in place** in those two files; this document records each finding, its severity, and the
> edit made, so the change is legible without a diff.
>
> **Audience framing:** the refined docs are written for a **public/OSS** release — org-specific
> identifiers were neutralized.
>
> **Repository note:** `joe2far/karo` is a **monorepo** that houses the operator, the CLI, the shared
> `karo-runtime`, and the agent image. The original PRDs assumed three separate repos; the refined
> v2 §13 now describes the monorepo layout (one JSON Schema, atomic cross-component changes).
>
> **KARO v1 reference:** v1 (`joe2far/karo-v1`, archived) was consulted read-only via the public web.
> It is a Go operator built around **15 CRDs**; v2 consolidates that surface into a single
> self-contained `AgentTeam` (+ `AgentTask`, + deferred `AgentChannel`). The new **v2 §2.1
> "Relationship to KARO v1"** documents the full CRD mapping, including the two intentional cuts
> (`EvalSuite`→reviewer agents, `AgentLoop`→deferred).

## How to read severity

- **P0 — parity break:** the same `AgentTeam` would behave differently, or fail to load/apply,
  locally vs on cluster. These directly undermine the product thesis.
- **P1 — implementation blocker:** a referenced field/behavior is missing or undefined, so an
  implementer cannot build it as written.
- **P2 — clarity / correctness / OSS-readiness.**

---

## P0 — parity breaks

| ID | Finding | Edit applied |
|---|---|---|
| **N2** | `runtime.backends` shape mismatch: CLI export §12 emitted `ref: <name>`; the operator CRD (v2 §3.1) expects `secretRef: { name, key? }`. A `karo export` would not `kubectl apply` cleanly — the headline "export == operator input" invariant, broken. | Unified on `{ kind, secretRef: { name, key? } }` in **CLI §12** and **v2 §3.1/§17**; added an explicit callout in both that the shape is shared and that bare `ref:` is removed. |
| **P1** | YAML `5_000_000` is not portable: PyYAML (1.1) parses it as int `5000000`; Go `yaml.v3` (1.2 core) parses it as the **string** `"5_000_000"` (empirically confirmed) — silent drift on the `int64` budget fields. Docs were also internally inconsistent (`5_000_000` vs `5000000`). | Replaced all `5_000_000` → `5000000`; added the **numeric-literals rule** (CLI §4.0) and a `karo validate` lint that rejects `_` in numeric scalars (CLI §7); added a large-int fixture to the parity test (CLI §19 / v2 §15). |
| **P2-canon** | "Byte-equivalent after canonicalization" is asserted as a CI invariant (CLI §0/§4.0.1/§7/§19; v2 §15) but the canonical form was never defined; two YAML emitters (Py/Go) can't match byte-for-byte. | Defined the **canonical JSON projection** in CLI §4.0.1 (sorted keys, plain integers, default-materialization policy, block-scalar normalization, UTF-8/newline rules) and made it the comparison basis in both parity tests. |
| **A1** | Cluster harness reality: `cursor`/`codex`/`claude-code` attach is a native interactive TUI/PTY; they can't run as headless agent pods, yet v2 claimed "any harness on cluster." A `harness: cursor` team validates locally but can't deploy. | Added the **harness portability matrix** (CLI §4.7) — `sdk` is the only guaranteed cluster harness; others local-only — gated by `HarnessCapabilities.clusterCapable` (CLI §6.2) and enforced by `karo validate --target cluster` / `karo export`. Reflected in v2 G5, §4.1, §5.1, §7. |
| **A3/§5.6** | Budget enforcement via "subscribe to usage metrics" (v2 §5 step 6) is eventually-consistent → N parallel pods overspend, and diverges from the CLI's pre-turn `can_spend` gate. | Made enforcement **authoritative and synchronous**: an atomic counter (`INCRBY` on cluster, file-lock locally) in `karo-runtime`, identical both sides (CLI §8, **new v2 §5.6**). Metrics demoted to observability only (v2 §9). |
| **A5** | `permissionMode: prompt` contradicts "not an approval workflow" (CLI §13) and is meaningless headless (no TTY) on cluster. | Added **CLI §4.2.1** distinguishing `permissionMode` (tool-exec policy) from `autonomy`/`guards` (human steering); forbade `prompt` + `autonomous`; defined cluster coercion/rejection at export. Cross-referenced in v2 §7. |

---

## P1 — implementation blockers (referenced but undefined)

| ID | Finding | Edit applied |
|---|---|---|
| **A6** | `pipeline` ordering is "via TaskGraph edges declared in spec" (CLI §11) but no schema field exists to declare it. | Added `coordination.pipeline.stages` (CLI §4.2), required iff `pattern: pipeline`, acyclic, names resolve (CLI §4.3); it projects onto `AgentTask.dependsOn` (CLI §11, v2 §3.2/§6). |
| **A4** | Swarm/parallel task claiming had no atomicity story → double execution. | Specified **atomic claim** (`FOR UPDATE SKIP LOCKED` + lease) for Postgres and a lockfile/atomic-rename equivalent for the file backend, in the shared contract test (CLI §11, v2 §3.2/§6/§15). |
| **A7** | The agent-pod entrypoint "reads config from env/CRD" (v2 §13) was underspecified — a single-agent pod can't know which agent it is or how to reach backends. | Defined the **bootstrap contract** (`KARO_TEAM`, `KARO_AGENT`, `KARO_RUN_ID`, backend DSNs from `secretRef`s, OTel endpoint) in **v2 §4.1**. |
| **A2** | Scale-to-zero (v2 §5.1) reclaims an idle agent, but a guard-paused agent awaiting attach with `pauseTimeout: 0` looks idle → deadlock (no pod to attach to). | Made **paused-for-attach agents exempt** from idle reclamation (default), with re-provision-on-attach as the documented fallback (v2 §5.1, §7; CLI §13). Added a scale-to-zero test (v2 §15). |
| **A8** | `--skills-bundle configmap` (default) breaks past the ~1 MiB ConfigMap cap, and `pack:` refs resolve live locally but aren't vendored for a "self-contained" CRD. | Changed default bundle to **`oci`** and specified that export **resolves and pins `pack:` refs by digest** (CLI §7/§9/§12, v2 §3.1). |
| **S1** | `budgets.perAgent: true` promises "unless overridden" but no per-agent budget field existed. | Added optional `agents[].budget: { share | limit }` and the split algorithm (CLI §4.2/§8) with validation (CLI §4.3). |
| **S2** | `model.profile` referenced (CLI §14) but absent from the model schema. | Added `model.profile` to the binding (CLI §4.2), documented resolution order and its local-only nature (CLI §4.5/§14; v2 §8). |
| **S3** | Cross-field validation rules (lead-iff-pattern, pipeline-iff-pattern, guard/`pauseOn` validity, budget sums) were unstated → two validators could disagree. | Enumerated them in **CLI §4.3** and required they live in the shared JSON Schema (`if/then`) so the operator's CRD validation agrees (v2 §5 step 1). |
| **S4** | `karo export` had no `--namespace`, but the CRD is namespaced (v2 §3.1) and teams isolate by namespace (v2 §9). | Added `karo export --namespace` (CLI §7/§12); namespace lives outside the `spec` parity comparison (CLI §12, v2 §3.1). |
| **S5** | `karo validate` promised "guard matcher validity" with no grammar. | Defined the **guard matcher grammar** (exact names, globs, `mcp:<server>/<tool>`; non-resolving = error) in CLI §13, referenced from §4.3. |
| **S7** | Both docs depend on a shared event vocabulary (CLI §15, v2 §9) that was never enumerated. | Added the **canonical event-vocabulary table** (CLI §15) and pointed v2 §9 metrics/traces at it. |
| **P3** | `${env:}`/`${file:}` interpolation (CLI §4.6) had no defined cluster mapping for export. | Added the **export transform table** (CLI §4.6): `${secret:}`→`secretRef`, `${env:}`→declared pod env or rejected, `${file:}`→inlined; reflected in v2 §3.1/§10. |

---

## P2 — clarity, correctness, OSS-readiness

| ID | Finding | Edit applied |
|---|---|---|
| **N1** | `team.yaml` vs `karo.yaml` used interchangeably across CLI §5/§7/§14/§17/§19. | Fixed the vocabulary once (CLI §0/§4.0/§4.1): `karo.yaml` = thin folder manifest; `team.yaml` = flat/compiled single-file. Swept the loose uses. |
| **N3** | `onExceed` enum stated inconsistently (`warn` missing in v2). | Enumerated `warn|pause|hardstop` in CLI §8/§4.2 and v2 §5.6, with cluster `warn` defined. |
| **N4** | `spec.{memory,coordination}.backend` (local) vs `runtime.backends` (cluster) precedence was implicit. | Documented that local `backend` selects the dev store and `runtime.backends` overrides on cluster (CLI §4.2/§10, v2 §6). |
| **N5** | v2 §3.3 called `AgentChannel` a "carry-over from KARO v1", but v1's `AgentChannel` was *external* integrations (Slack/etc.), not inter-agent messaging. | Corrected **v2 §3.3** to state the new cross-team-messaging meaning and drop the inaccurate carry-over claim. |
| **N6** | §4.4 promised to "reject unknown apiVersion" without listing the accepted set or forward-compat behavior. | Stated the accepted set (`karo.dev/v1` only) and the reject-don't-coerce rule (CLI §4.4, v2 §3). |
| **M1** | v2 §9 cited a non-existent "Section 5.6"; budget enforcement was fragmented. | Created an authoritative **v2 §5.6** and fixed the §9 pointer. |
| **D1** | The 15→2 CRD consolidation was never explained, making v2 hard to understand from the docs alone. | Added **v2 §2.1 "Relationship to KARO v1"** with the full CRD mapping table and the two intentional cuts. |
| **O1/O2** | Org-specific identifiers throughout (employer name, personal owner/author, real-looking cloud profiles, a specific marketplace pack). | Genericized: removed the employer and personal-name references; `owner: platform`; `awsProfile: my-aws-profile`; `project: my-gcp-project`; `pack:example/python-development`. Added a CLI §7/§21 note that `karo init` templates are CI-checked for org identifiers. |

---

## Refuted / non-issues

- **`apiVersion` group** — `karo.dev/v1` is used consistently in both docs; not a bug. Only the
  *accepted-set documentation* was thin (handled by N6).

---

## Out of scope (follow-ups, not done here)

- `LICENSE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md` for the OSS release — recommended before going
  public, but not part of this spec review.
- Writing the JSON Schema, pydantic models, Go types, or any implementation code — this pass refines
  the *specs* only; M0 (CLI §18 / v2 §14) produces those artifacts.
- Re-architecting the product (new patterns/CRDs, changing the thesis) — the brief was to refine and
  complete the existing design, not redesign it.
