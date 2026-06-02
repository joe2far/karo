# 06 — M2 (Coordination) Inventory

Same review treatment as M0, now for M2. State key: **T** tested · **U**
implemented-unverified · **M** missing/stub · **P** partial. Cluster-dependent
paths run on the user's kind+Postgres; verified-here items note the evidence.

## M2 deliverable inventory (CLI §18 M2 / v2 §14 v2-M2)

| # | M2 item | Path(s) | State | Evidence |
|---|---|---|---|---|
| 1 | Coordinator: atomic task claim in the run loop | `runtime/coordinator.py` (claim loop); `stores/file.py` `claim`; `stores/postgres.py` `claim` | **T** | run loop claims per-agent (`coordinator.py` run); atomic-claim concurrency test `tests/test_stores.py::test_atomic_claim_no_double_execution` (file now, postgres on kind) |
| 2 | lead-and-teammates: decompose → handoff → review → synthesize | `runtime/patterns.py`; `runtime/coordinator.py` | **T** | `test_coordinator.py::test_lead_crew_decomposes_and_synthesizes`, `::test_lead_crew_runs_to_completion_with_handoff`, `::test_lead_and_teammates_review_state`; end-to-end smoke shows implementer→review→done + mailbox handoff |
| 3 | `review` task state (reviewer agent) | `stores/base.py` `TaskState.review`; `coordinator.py` review block; `Task.reviewer` | **T** | review transition emitted + reviewer turn + memory; tested |
| 4 | Mailbox handoff (assign / teammate→lead / reviewer→lead) | `coordinator.py` `_send_mail`; `EventType.mailbox_send` | **T** | handoff test asserts worker→planner inbox; events emitted |
| 5 | Durable tasks/mailbox/memory on Postgres | `stores/postgres.py` (Task/Memory/Mailbox) | **U** | implement same Protocols; run the SAME contract via `store_contract` (skip without `KARO_TEST_PG_DSN`) |
| 6 | Postgres is the single default backend | `store_contract.py`, `entrypoint.py`, Dockerfile, helm values, sample | **T** | file+postgres in contract; redis kept optional |
| 7 | `karo tasks / mail / memory` | `cli/karo/cli.py` | **T** (tasks/mail) | `test_cli.py` exercises tasks/mail; manual smoke shows mail handoffs |
| 8 | Resume (`karo run --resume`) | `coordinator.py` `plan()` reuse | **P** | local resume reuses persisted tasks (`test_resume_keeps_existing_tasks`); **kill-mid-run→resume parity not tested** |
| 9 | ★ Parity Checkpoint A (file == Postgres) | `tests/test_parity.py` | **P** | spec-parity + local determinism **T**; cross-backend runtime parity **skips without `KARO_TEST_PG_DSN`** (runs on kind) |
| 10 | Operator: provision agent pods + full team spec + objective | `operator/internal/controller/provisioner.go` | **T** | per-team ConfigMap with full `team.json` + `KARO_OBJECTIVE`; **envtest reconcile test PASSES against a real apiserver** |
| 11 | Operator: kill-all-pods → reconcile → resume | (relies on durable Postgres + entrypoint) | **U** | resumable by construction; not e2e-tested (needs kind) |
| 12 | Dispatcher (task pump / scale-up) | `operator/internal/dispatcher/dispatcher.go` | **M** | still a scaffold; cluster coordination relies on per-pod claim against shared Postgres instead |

## Cross-cutting verification (this environment)
ruff clean · pytest 62 + 11 · `go build/vet/test` green · **envtest reconcile
PASS against real apiserver** (`KUBEBUILDER_ASSETS` 1.29.0) · golangci-lint 0 ·
controller-gen + schema drift in sync.

## Post-review state updates (after `07-m2-verdict.md` fixes)

The adversarial review (07) found the cluster lane over-claimed; after fixes,
**validated against a real Postgres**:

| # | item | was | now | note |
|---|---|---|---|---|
| 5 | Durable Postgres stores | U | **T** | all store-contract tests pass on real Postgres (JSONB verified) |
| 8 | Resume | P | **T** | `test_resume_after_partial_run_completes` (kill→resume) |
| 9 | Parity Checkpoint A | P | **T** | **file == Postgres** task graph + outputs passes; CI runs `postgres:16` |
| 12 | Dispatcher / scale-up | M | **M (M3)** | coordination via pod-side claim; on-demand scale-up is M3 |
| + | multi-pod double-plan guard | — | **T** | lead-only plan + agent-scoped claim |
| + | budget-pause vs guard-release | — | **T** | `Task.pause_reason` distinguishes them |

(Verdict and critical-issue analysis: `07-m2-verdict.md`.)
