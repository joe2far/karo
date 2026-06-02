# 02 — M0 Fix Plan

Scope: **M0 only.** Each item is *issue · evidence · impact · fix sketch*. Items
deferred to M1/M2 are listed at the end so the boundary is explicit (no scope-creep).
Work lands on `claude/karo-m0-review-WqdmP` in small finding-referenced commits.

## Must-fix (gate: build + lint + tests + drift-check green)

### F1 — Go CRD is a lossy subset of the shared spec; prunes `resources`/`memory` on apply  *(CRITICAL)*
- **Evidence:** Go `AgentTeamSpec` omits `resources`, `memory`,
  `coordination.mailbox`/`taskLayer`, `agents[].permissionMode`/`budget`/`memory`,
  `model.params` (`operator/api/v1/agentteam_types.go:146`); generated CRD `spec`
  props are only `[agents,budgets,coordination,defaults,interaction,runtime]`; the
  `x-kubernetes-preserve-unknown-fields` comment (`:7-11`) is false (`grep` → none).
- **Impact:** `kubectl apply` of a real `karo export` silently drops MCP servers,
  skills, tools, memory config — the core parity break.
- **Fix:** model the full shared spec body in Go (add `Resources`, `Memory`,
  `MailboxConfig`, `TaskLayer`, `AgentBudget`, `AgentMemoryRef`, agent
  `permissionMode`, `model.params`); free-form `model.params`/`tool.schema` via
  `RawExtension` + `Schemaless`+`PreserveUnknownFields`. Remove the false comment.
  Regenerate deepcopy + CRDs with controller-gen.

### F2 — No Go ⇄ JSON-Schema conformance check (the parity bridge is missing)  *(CRITICAL)*
- **Evidence:** nothing in `operator/` reads `schema/agentteam.schema.json`; CI has
  only pydantic⇄schema and Go⇄CRD halves (`ci.yml:25-66`).
- **Impact:** Go types drift from the schema undetected (F1 is exactly that).
- **Fix:** add a Go test (`api/v1/schema_parity_test.go`) that loads the shared JSON
  Schema and the generated CRD and asserts the CRD `spec` covers every schema spec
  property + key enums. Runs under existing `go test ./...` (no new CI plumbing, no
  new deps — uses `sigs.k8s.io/yaml`).

### F3 — CI schema drift-check fails on a clean tree (B1)  *(CRITICAL)*
- **Evidence:** `ci.yml:28` `head -c -1` strips the committed file's trailing newline
  → spurious diff on byte-identical schema (`00-build.log`).
- **Impact:** the one Python-side parity gate is permanently red; real drift is
  indistinguishable from the bug.
- **Fix:** compare with a robust normalized `diff` (regenerate to a file and
  `diff -u generated committed`, both newline-terminated).

### F4 — Cross-field `if/then` rules absent from schema + CRD (§4.3)  *(CRITICAL)*
- **Evidence:** `grep if/then/allOf agentteam.schema.json` → 0; CRD has no
  `x-kubernetes-validations`; controller `validateTeam` checks only
  apiVersion/agent-count/harness (`agentteam_controller.go:81`).
- **Impact:** operator accepts specs `karo validate` rejects (lead missing under
  lead-and-teammates; pipeline.stages missing; prompt+autonomous).
- **Fix:** (a) inject `allOf`/`if-then` for the two coordination rules into the
  generated JSON Schema (`schema_export.py`); (b) mirror the rules in the controller
  `validateTeam` (lead-iff-pattern, stages-iff-pattern, prompt+autonomous) so the
  operator rejects them. Regenerate committed schema.

### F5 — `karo compile` crashes in default yaml mode (B2)  *(CRITICAL)*
- **Evidence:** `cli.py:117` `model_dump(...)` keeps pydantic enums; `:121`
  `yaml.safe_dump` raises `RepresenterError(<Backend.file>)`.
- **Fix:** dump with `mode="json"` so enums serialize to strings. Add a CLI test
  asserting `karo compile` (yaml) succeeds.

### F6 — Operator never reaches the M0-specified `Pending` status  *(CRITICAL, M0 acceptance bar)*
- **Evidence:** `agentteam_controller.go:67` sets `Phase="Running"`; `Pending` never
  assigned; no envtest reconcile test (so unnoticed).
- **Fix:** set `Phase=Pending` when no agents are active yet (scale-to-zero / not yet
  provisioned), `Running` once `activeAgents>0`. Add a unit test on the phase logic.

### F7 — Store "shared contract test" claimed but absent; file-only suite  *(HIGH, M0 Protocol deliverable 5b)*
- **Evidence:** `stores/base.py:5` claims a shared contract suite; `test_stores.py`
  hard-codes the file backend, not parametrized.
- **Impact:** redis/pg (M1) would share zero assertions → backend parity unverifiable.
- **Fix:** extract the contract bodies into a reusable, store-factory-parametrized
  module (`tests/store_contract.py`) and run the file backend through it; M1 adds
  redis/pg params. No behavior change to stores.

### F8 — Missing M0 verification tests (cheap, high value)  *(HIGH)*
- `include:` deep-merge local-wins — untested (`loader.py:78`).
- Secret interpolation primitives — untested (`loader.py:97`).
- Folder-compile == flat-`team.yaml` round-trip after canonicalization — missing
  (§4.0.1).
- **Fix:** add focused unit tests for each.

### F9 — Lint hygiene  *(LOW)*
- 16 ruff findings (`00-build.log`). **Fix:** `ruff --fix` + resolve `E741`.

## Deferred — NOT M0 (documented for M1/M2 so we don't scope-creep)
- **Adapter `attach()` real seam** (stream+inject+interrupt) → CLI-M1.
- **Coordinator must call atomic `claim()`** instead of `_next_runnable` →
  coordination is M2; tracked in `05-verdict.md` as a pre-M2 must-fix.
- **`AgentContext.memory/mailbox/budget` injection** → M1 (needed when adapters use them).
- **Diagnostic line precision** (`AGENT.md:3`, currently `line=0`) → high-priority,
  needs frontmatter position tracking; M1 polish.
- **envtest reconcile test + kind e2e** → M1 operator lane (this environment lacks
  kind/envtest; will run on user's kind).
- **`budgets.perAgent` equal-remainder split** → M1 budget work.

## Exit criteria for the gate
`pytest` (runtime+cli) green · `go build/vet/test` green · `golangci-lint` clean ·
`ruff` clean · CI schema drift-check passes on a clean tree · Go⇄schema parity test
passes · updated states in `00-inventory.md`.
