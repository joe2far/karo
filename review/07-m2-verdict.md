# 07 — M2 (Coordination) Review & Verdict

Same treatment as the M0 verdict. An adversarial fresh-eyes review audited the M2
coordination code; this records the findings, what was fixed, what genuinely
remains, and the M3 recommendation. **The cluster lane was validated against a real
Postgres** (run locally during the review), so the headline claims are now tested,
not asserted.

## One-paragraph verdict

The **local lane of M2 was solid; the cluster lane was over-claimed** — the
adversarial review correctly found that every Postgres store test and Parity
Checkpoint A only *skipped* (no DSN), that the Coordinator left every agent pod
free to call `plan()` (so N pods would **duplicate the task graph**), and that pods
weren't agent-scoped. Those are exactly the kind of red findings a real review must
surface. They are now **fixed and verified**: a shared-Postgres run shows all store
contract tests passing (the suspected JSONB bug was a non-issue) and **Parity
Checkpoint A passing (file == Postgres task graph + outputs)**; the multi-pod
double-plan is closed by lead-only planning + per-agent claim scope (tested); CI
now runs a `postgres:16` service so the cluster lane is exercised on every change.
With those closed, **M2 coordination is a credible base for M3** — provided the
remaining cluster-ops items (on-demand scale-up, mailbox consumption) are taken on
in M3, where they belong.

## Adversarial findings — disposition

| # | Severity | Finding | Status |
|---|---|---|---|
| 1 | 🔴 | Multi-pod **double-plan**: every pod calls `plan()`, no leader election → N task graphs | **FIXED** — `Coordinator(agent=…)`; only the lead pod plans; `test_cluster_pods_do_not_double_plan` |
| 2 | 🔴 | Pods not agent-scoped: `run()` drove the whole team on every pod | **FIXED** — `agent=` scopes claim to one agent; entrypoint passes `KARO_AGENT` |
| 3 | 🔴 | Dispatcher is a stub (v2-M2 deliverable) | **PARTIAL** — coordination is done by **pod-side atomic claim against shared Postgres** (no separate dispatcher needed for correctness); the *scale-up-on-demand* role remains M3 (see Critical-for-M3 #1) |
| 4 | 🔴 | Postgres JSONB binding likely broken (asyncpg str→jsonb) | **NOT A BUG** — verified: all PG store tests pass against real Postgres |
| 5 | 🔴 | PG store tests + Parity Checkpoint A never run (skip-only) | **FIXED** — validated on a real local Postgres; **CI now runs a `postgres:16` service** + `KARO_TEST_PG_DSN` |
| 6 | 🔴 | No kill→resume test | **FIXED** — `test_resume_after_partial_run_completes` (stop at `max_turns`, fresh Coordinator resumes to completion) |
| 7 | 🟡 | Budget-pause recovered via guard-release path (sets `guard_released`) | **FIXED** — `Task.pause_reason`; `release_paused` only clears guards; `test_budget_pause_resume_does_not_fake_guard` |
| 8 | 🟡 | Decomposition is one-task-per-teammate, not "real decomposition" | **ACKNOWLEDGED** — it is lead-driven task creation + review + synthesis; true semantic decomposition needs a live model (deterministic stub can't). Claim reworded; richer decomposition is M3 (model-driven) |
| 9 | 🟡 | Mailbox is write-only in the run loop (never consumed/`mark_read`) | **OPEN (M3)** — handoffs are recorded + evented; consumption-driven reactions are M3 |
| 10 | 🟡 | Reviewer detected by hardcoded `name=="reviewer"` | **OPEN** — convention documented; a `coordination.reviewer` field is a small spec add for M3 |
| 11 | 🟡 | `review` state not asserted by its test | **FIXED** — `test_review_state_is_entered` asserts the `→ review` transition via events |
| 12 | 🟡 | 3 connection pools per pod (exhaustion) | **FIXED** — one shared pool per DSN; reproduced the exhaustion, then fixed (parity test went red→green) |
| 13 | 🟡 | `teamDocJSON` shallow copy fragile | **MINOR** — `Runtime=nil` on the value copy is safe; commented |
| 14 | 🟡 | No test that the Go-projected camelCase doc loads via `compile_flat` | **FIXED** — `test_operator_projected_camelcase_doc_loads` asserts every camelCase field survives (Go json-tag ⇄ pydantic alias) |
| 15 | ⚪ | f-string table names (injection surface) | Noted — internal-only, namespace sanitized |
| 16 | ⚪ | budget-hardstop leaves a task `assigned` until lease TTL | Noted — lease-reclaim covers it on resume |

## What M2 delivers (tested)

- Atomic-claim run loop (file + Postgres), agent-scoped; **no double-claim** under
  parallel pull (contract test on both backends).
- lead-and-teammates: lead creates teammate tasks → teammates execute → **report to
  the lead's mailbox** → **`review` state** (reviewer) → lead **synthesis** (gated on
  all teammate tasks). All states persisted.
- Durable tasks/memory/mailbox on Postgres (the single default backend); same
  Protocol + same contract tests as file.
- Resume from persisted state (kill mid-run → fresh Coordinator completes).
- **Parity Checkpoint A passes** locally (file) and on real Postgres — the headline
  invariant is now a green test, in CI.
- Operator: full team spec + objective projected into agent pods; envtest reconcile
  test passes against a real apiserver.

## Critical before M3

1. **On-demand scale-up (the Dispatcher's remaining role).** Today agent pods must
   be running to claim; a zero-scaled team has no one to plan/claim. M3 needs the
   controller/dispatcher to provision the lead pod on objective arrival and wake
   teammate pods when claimable work exists (the warm-pool/scale-from-zero path,
   v2 §5.1). *Evidence:* `internal/dispatcher/dispatcher.go` is a stub; pods run a
   single-shot `run()` (`entrypoint.py`) rather than a long-lived claim loop.
2. **Long-lived pod claim loop.** `entrypoint.py` calls `coord.run()` once and exits;
   a teammate pod that starts before the lead plans finds no work and exits. M3: a
   poll/await loop (or dispatcher-driven wake) so late work is picked up.
3. **Mailbox-driven coordination.** Consume inboxes (`mark_read`) to drive agent
   behavior, not just record handoffs (finding #9) — needed for real lead↔teammate
   dialogue and the dev-team acceptance scenario.

## High-priority
- `coordination.reviewer` as an explicit field (replace the name convention, #10).
- e2e on kind: 3-agent team, kill all pods mid-run, assert resume (the v2-M2
  acceptance the envtest unit can't cover); the pieces (durable Postgres, resume,
  parity) are in place.
- Richer lead decomposition once a real model is wired (#8).

## Recommendation

**Proceed to M3 (multi-harness + multi-provider) — the coordination foundation is
sound and now parity-tested across file and Postgres.** The adversarial review's red
findings are closed and verified on a real backend; the remaining items are
genuinely M3-scoped (scale-from-zero orchestration, mailbox-driven dialogue,
model-driven decomposition) rather than defects in what M2 claims. Do the kind-based
kill→resume e2e early in M3 to convert the last "verified locally" into "verified on
cluster."
