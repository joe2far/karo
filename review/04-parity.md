# 04 — Parity Checkpoint A (scaffolded) + the M2/M3 acceptance gate

Parity — *"the same `AgentTeam` runs locally and on Kubernetes"* — is the product
thesis, so it is stood up as a **tested invariant now**, at M1, not bolted on later.
This file records what is enforced today and the precise contract M2/M3 must meet.

## What's enforced today

### Spec-level parity (PASSES, M0/M1)
- **Go ⇄ JSON-Schema:** `operator/api/v1/schema_parity_test.go` fails CI if the
  CRD drops any shared-schema spec property (the F1/F2 bridge). Negative-control
  verified.
- **Export round-trip:** `karo-runtime/tests/test_export.py` — the canonical
  projection of the local compiled spec equals the exported `spec` after
  canonicalization; secrets never emitted as values; large ints plain-decimal.
- **Canonical projection determinism:** `tests/test_parity.py::test_spec_parity_projection_is_stable`.

### Runtime determinism (PASSES, M1)
- `tests/test_parity.py::test_local_run_is_deterministic` — the same fixture run
  twice with file stores yields an identical task-graph signature (the precondition
  for cross-backend parity).

### The store-contract bridge (PASSES file; cluster runs on kind)
- `tests/store_contract.py` runs the **same** conformance + atomic-claim tests
  against file (now) and Redis/Postgres (when `KARO_TEST_REDIS_URL` /
  `KARO_TEST_PG_DSN` are set). This is what guarantees the backends behave
  identically under the parity run.

## Parity Checkpoint A — scaffolded, `xfail` until M2

`tests/test_parity.py::test_parity_checkpoint_a_runtime` runs the deterministic
fixture (`tests/fixtures/parity_team.yaml`, a 3-stage pipeline with the stub
harness) **locally (file stores)** and **on cluster (Redis + Postgres)** and
asserts an **identical task graph + outputs**. It is marked `xfail(strict=False)`
with the exact outstanding work, and `fail`s with guidance when cluster backends
aren't configured — so it can never silently "pass" by doing nothing.

**Exactly what M2 must deliver to make it xpass** (from `M2_REQUIREMENTS`):
1. Coordinator drives `lead-and-teammates` with **real lead task decomposition**
   (today `patterns.plan_tasks` does one-task-per-teammate — structural, not a
   lead decomposing an objective).
2. **Atomic claim wired into the run loop** — the Coordinator must call
   `store.claim()` (already implemented + contract-tested) instead of the
   non-atomic `_next_runnable` scan, so file and Postgres agree under parallel pull.
3. **Mailbox-mediated handoff** (lead → teammates → lead) with deterministic
   ordering, exercised by the run.
4. **Resume parity** — kill mid-run, resume from committed state, identical
   terminal graph on both backends (the production form of `--resume`).
5. **Cluster execution path** — operator Dispatcher + agent pods produce the same
   graph as local; needs the kind stack (Redis/Postgres) the user runs.

## How to run it for real (on the user's kind cluster)

```
# bring up redis + postgres (e.g. in kind), then:
export KARO_TEST_REDIS_URL=redis://localhost:6379/0
export KARO_TEST_PG_DSN=postgresql://karo:karo@localhost:5432/karo
pytest karo-runtime/tests/test_stores.py     # store contract on real backends
pytest karo-runtime/tests/test_parity.py     # Parity Checkpoint A (xpass after M2)
```

## The M2/M3 acceptance gate (record only — DO NOT build now)

The eventual headline acceptance test (analogous to the older review's
"dev-team" scenario) is a **multi-agent dev-team objective run end to end across
the lifecycle** — *design → implement → review → close* — using:
- `lead-and-teammates` with a `reviewer` agent (the `review` task state) and a
  `pauseOn`/`pauseBefore` guard exercising attach-and-direct;
- the `task` lifecycle `pending → assigned → in-progress → review → done` with
  mailbox handoffs and at-least-once delivery;
- run **locally** (file stores) and **on kind** (Redis/Postgres) and assert the
  **same task graph, same terminal outputs, and same attach/guard behavior**
  (Parity Checkpoint B: `karo export` → `kubectl apply` → identical behavior).

This is the M2 (coordination) → M3 (multi-harness/provider) target. It is **not**
stubbed at M1 — the scaffold above is the seam it will fill.
