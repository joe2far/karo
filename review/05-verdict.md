# 05 — Verdict & Prioritized Plan

## One-paragraph verdict

**As found (pre-fix): no — the M0 foundation was not yet safe to build M1+ on, for
one specific reason.** The Python core was genuinely good (clean spec models,
deterministic folder compiler, canonicalizer, file stores, authoritative budget,
56 passing tests, no `charter`/`hitl` remnants, correct `karo.dev/v1`), but the
single invariant the whole product rests on — *"the same `AgentTeam` runs locally
and on Kubernetes"* — was **unenforced and already violated**: the Go CRD was a
hand-written lossy subset that **pruned `spec.resources`/`spec.memory` on
`kubectl apply`**, the comment claiming a `preserve-unknown-fields` safety net was
fabricated, no CI job compared the Go types to the schema, the one Python-side
drift gate was broken on a clean tree, and `karo compile` crashed. **After the M0
fixes (Phase 2): yes** — the CRD now carries the full spec body, a Go⇄JSON-Schema
parity test fails CI on any future drift (negative-control verified), cross-field
rules are enforced on both lanes, and the build is green end to end. With M1 now
landed on both lanes against the shared library and Parity Checkpoint A scaffolded
as a tested `xfail`, **this is a foundation I would tell a peer to build M2 on** —
provided the four "critical before M2" items below are closed.

## Strengths (vs. kagent / dapr-agents / LangGraph deploys / Managed Agents / Gas City)

1. **Parity is now a *tested* invariant, not a slogan.** `schema_parity_test.go`
   (Go⇄schema) + the export round-trip + the canonical projection mean the
   local↔cluster spec contract is enforced in CI. This is the differentiator vs.
   single-vendor Managed Agents and UI-locked Agent Teams, and most K8s agent
   projects don't even attempt it.
2. **One shared behavioral library** (`karo-runtime`) genuinely imported by both
   the CLI and the agent image — the Go controller holds **zero** agent-reasoning
   logic (verified). The hard Go/Python boundary the PRD demands actually holds.
3. **Backend-pluggability is contract-tested**, not asserted: the same
   conformance + atomic-claim suite runs against file (now) and Redis/Postgres
   (on kind), so "file locally, redis+pg on cluster" is provable.
4. **Authoritative, synchronous budget counter** with no-overspend-under-
   concurrency test — cost-runaway control that's real, not a dashboard.
5. **Attach-and-direct, not approval checkpoints** — a real `AttachSession`
   (stream/inject/interrupt/detach) and pause-and-flag guards, matching how devs
   actually drive Claude Code/Cursor/Codex.

## Critical issues (must-fix before M2)

1. **Coordinator must use the atomic `claim()` in its run loop.**
   *Evidence:* `runtime/coordinator.py` `_next_runnable` (non-atomic scan); `claim()`
   exists + is contract-tested but has zero callers. *Impact:* file and Postgres
   diverge under parallel pull; Parity Checkpoint A cannot pass. *Fix:* replace the
   scan with `store.claim(owner)`; swarm/parallel patterns then inherit atomicity.
2. **Real `lead-and-teammates` decomposition + mailbox handoff.**
   *Evidence:* `runtime/patterns.py` `plan_tasks` does one-task-per-teammate, no
   lead decomposition, no mailbox-mediated handoff. *Impact:* the headline pattern
   is structural only; the dev-team acceptance scenario can't run. *Fix:* lead
   decomposes the objective into tasks → mailbox to teammates → reviewer (`review`
   state) → lead synthesis.
3. **Close the operator provision→run join.** *Evidence:* `agentConfigMap` projects
   a single agent (`json.Marshal(agent)`) but `entrypoint.py` `compile_flat`
   expects a full team document; the Dispatcher is a scaffold and never sets
   `KARO_OBJECTIVE`. *Impact:* a provisioned pod idles; no one-agent objective
   completes on cluster. *Fix:* project the full compiled team into the ConfigMap
   (or load the agent slice in the entrypoint) and implement the Dispatcher task
   pump that scales up + sets the objective.
4. **Resume parity across a pod kill.** *Evidence:* CLI `--resume` keeps tasks
   (`coordinator.plan`), but no test kills mid-run and asserts identical terminal
   graph on both backends. *Impact:* the production resumability claim is unverified.
   *Fix:* the parity test's kill-and-resume assertion (already listed in
   `M2_REQUIREMENTS`).

## High-priority (before public/external exposure)

1. **Diagnostic line precision.** `validate.py` emits `line=0` for every cross-field
   diagnostic (`frontmatter.py` discards positions). PRD wants `AGENT.md:3`. *Fix:*
   track frontmatter/field line offsets through compile into diagnostics.
2. **Helm chart completeness.** No Dispatcher Deployment, no Redis/Postgres
   subcharts, no OTel wiring (`charts/karo/values.yaml` flags are dead config).
3. **Mailbox at-least-once is really send-once.** No redelivery/ack; document or
   implement before relying on the guarantee under swarm.
4. **`budgets.perAgent` equal-remainder split** is modeled but unimplemented
   (`validate.py` only checks overcommit).

## Nice-to-have

1. Adopt `ruff` (and `mypy`) as actual CI gates (config + workflow step) — the code
   is now clean; lock it in.
2. `BUILTIN_DEFAULTS` (`schema_export.py`) is a hand-kept duplicate of pydantic
   field defaults with no agreement test — add one or derive it.
3. e2e on kind (`operator/test/e2e`) is still a `t.Skip` placeholder.

## What the PRDs claim that the code doesn't yet have

| PRD claim | Code reality |
|---|---|
| §4.3: cross-field rules "expressed in the shared JSON Schema (`if/then`)" | Now partially true — coordination `if/then` injected (Phase 2); reference-resolution + prompt/autonomous remain imperative (Python + Go controller), which is acceptable. |
| §11 / §6: "all three patterns run on the same Coordinator primitives" with atomic claim | Patterns *plan*; the run loop doesn't use atomic claim yet (Critical #1). |
| v2 §4.1: agent pod "runs the karo-runtime Coordinator loop for that single agent" | Entrypoint can, but provisioning projects a single agent + no objective dispatch (Critical #3). |
| v2 §6: "kill-all-pods → reconcile → resume" | Resume primitive exists; cross-kill parity untested (Critical #4). |
| §13 / v2 §7: attach over the cluster API (websocket) | Local `AttachSession` is real; the cluster transport is documented, not built (M4). |
| §11: Helm installs Dispatcher + Redis/Postgres + OTel | Installs CRDs+controller+RBAC only (High-priority #2). |

## Recommendation

**Proceed to M2 after the four critical fixes above.** The foundation crack that
made the original M0 untrustworthy (lossy CRD / unenforced parity) is closed and
now guarded by a CI test, and M1 is real on both lanes against one shared library;
the remaining criticals are coordination-layer work that *is* M2 by the roadmap,
with the parity test already standing as the gate they must satisfy. Do **not**
ship externally until the High-priority items (line-precise diagnostics, Helm
completeness, mailbox semantics) are also closed, per the §4.4 "freeze before
external exposure" rule.
