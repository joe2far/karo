# 01 — M0 Foundation Review

Verdict scale: **Sound** · **Sound with caveats** · **Concerning** · **Broken**.
Evidence cites `path:Lnn`. Synthesis of direct inspection + three focused
sub-reviews (shared spec / CLI+harness+stores / Go operator).

---

## 1.1 Single source of truth — the parity guarantee — **BROKEN**

This is the one invariant that makes "same spec locally and on cluster" true
rather than a slogan (`PRD-KARO-CLI.md` §4.4; `PRD-KARO-v2.md` §16). It does not hold.

**What's right:** the JSON Schema is *generated from* the pydantic models
(`schema_export.py:38-44` `AgentTeam.model_json_schema`), so the Python lane has a
real single source. `karo init/validate` use the same models. apiVersion allowlist
is shared in spirit (`models.py:23`, `groupversion_info.go:28`).

**Why it's broken:**
1. **No Go ⇄ JSON-Schema bridge exists.** The Go CRD types
   (`operator/api/v1/agentteam_types.go`) are **hand-written**, not generated from
   `schema/agentteam.schema.json`. `grep agentteam.schema.json operator/` → nothing.
   CI (`ci.yml`) has two *independent* halves — pydantic⇄schema (`:25-29`) and
   Go-structs⇄generated-CRD (`:57-66`) — and **nothing compares Go to the schema**.
   The two types can drift forever and CI stays green.
2. **They have already drifted, lossily.** The Go `AgentTeamSpec` (`agentteam_types.go:146`)
   omits `resources`, `memory`, `coordination.mailbox`, `coordination.taskLayer`,
   `agents[].permissionMode`, `agents[].budget`, `agents[].memory`, `model.params`.
   Verified against the generated CRD: `spec` properties are only
   `[agents, budgets, coordination, defaults, interaction, runtime]`.
3. **The claimed safety net is fabricated.** `agentteam_types.go:7-11` says spec
   carries `x-kubernetes-preserve-unknown-fields` so the full schema round-trips.
   It does not — `grep preserve-unknown` over both CRD copies returns nothing.
   Structural CRDs **prune** unknown fields, so `kubectl apply` of a faithful
   `karo export` (which emits `spec.resources`/`spec.memory`/mailbox/taskLayer)
   **silently strips them**. That is the precise CLI⇄cluster parity break this
   milestone exists to prevent — and the project's headline pitch.
4. **The one Python-side gate is broken (B1).** The CI drift `diff` uses
   `head -c -1` on the committed file, which strips its trailing newline and makes
   `diff` fail on a byte-identical tree (`00-build.log`). So even the half that
   exists is red on a clean checkout.
5. **Cross-field `if/then` rules are not in the schema** (`grep if/then/allOf/oneOf
   agentteam.schema.json` → 0), despite `validate.py:5` and §4.3 asserting they are.
   They live only in Python `validate.py`; neither the schema nor the CRD encodes
   them, so the operator accepts specs the CLI rejects (§1.6, §9 below).

This is a **critical** finding. Until a Go-side schema conformance check exists and
the CRD carries the full spec (typed *or* via genuine preserve-unknown-fields), the
parity claim is aspirational and actively violated.

---

## 1.2 Compiled spec model (§4.2) — **Sound**

Matches §4.2 field-for-field (`models.py`): defaults→team→built-in resolution order
is implemented and tested (`canonicalize.py:35-68`, `test_spec.py:77`). Secret
interpolation is **lazy** — the compiler never resolves placeholders, so nothing is
persisted resolved (`loader.py:97-126`; compile path never calls `interpolate`);
the export boundary test confirms only `secretRef`s, never values, are emitted
(`test_export.py:79`). `include:` deep-merges with local-override-winning
(`loader.py:78-90`). `extra="forbid"` on every model (`models.py:30`) catches stray
fields. No `charter`, no `hitl`.

**Caveats (not blocking):** `include:` and the interpolation primitives have **no
direct tests**; `budgets.perAgent` equal-remainder split is modeled but unimplemented
(`validate.py:224` only checks overcommit); `BUILTIN_DEFAULTS` (`schema_export.py:15`)
is a hand-kept second copy of defaults with no agreement test.

---

## 1.3 Folder compiler (§4.0/§4.0.1) — **Sound with caveats**

Compilation is **deterministic** (sorted discovery, `compile.py:129,70,82,96`).
`AGENT.md` frontmatter **and body** both map — body → `instructions`
(`compile.py:159`). Auto-discovery of `agents/`, `skills/`, `tools/` (AST-only
`@tool` scan, never imports — `compile.py:40-59`), `mcp/` works
(`_discover_resources`). Canonicalization is solid and tested (§1.7).

**Caveats:**
- **Diagnostics map to source *file* but always `line=0`** (`validate.py:65`;
  `frontmatter.py` discards positions). PRD's `agents/planner/AGENT.md:3` precision
  is unmet for everything except the numeric-literal lint.
- **No round-trip test** that a folder compiles to the same canonical projection as
  the equivalent hand-written flat `team.yaml` — §4.0.1 explicitly claims this
  equivalence, and it's plausible to break (`frontmatter.body` is `.strip()`-ed at
  `frontmatter.py:42` while flat `instructions: |` keeps YAML's trailing newline).
- **`karo compile` crashes in default yaml mode (B2)** — `cli.py:121`
  `yaml.safe_dump` chokes on pydantic enums. `--format json` works.

---

## 1.4 Store Protocols — **Concerning**

The Protocols themselves are good: `MemoryStore`/`TaskStore`/`MailboxStore` are
clean, `@runtime_checkable`, backend-agnostic (`stores/base.py:98-122`) — a
redis/pg backend *could* implement them unchanged.

**Why concerning:**
- **The "shared contract test" the design depends on does not exist as such.**
  `stores/base.py:5` and `file.py:5` claim "both pass the same contract tests," but
  `test_stores.py` instantiates `FileTaskStore`/`FileMailboxStore`/`FileMemoryStore`
  **directly, not parametrized over a backend fixture**. There is one backend and
  the suite is welded to it; a future redis/pg backend shares **zero** assertions.
  This is the M0 mechanism that's supposed to *guarantee* backend parity (§13 v2),
  and it's prose, not code.
- **Atomic claim is tested but unused.** `FileTaskStore.claim` (lockfile + atomic
  rename + lease, `file.py:87-110`) passes an 8-worker no-double-claim test
  (`test_stores.py:27`), but the **Coordinator never calls `claim()`** — its run
  loop uses the non-atomic `_next_runnable()` scan (`coordinator.py:170,248`).
  So the safety primitive exists but the actual execution path it protects doesn't
  use it. `grep '.claim('` → tests only.
- Mailbox ordering + `hardLimit` GC are tested (`test_stores.py:85`); "at-least-once"
  is a stated design choice with no redelivery/ack mechanism — effectively
  send-once today (fine for M0, document it).

For M0 (file backend only) this is acceptable, but the conformance harness must be
parametrized **before** redis/pg land or the parity story is unverifiable.

---

## 1.5 `HarnessAdapter` Protocol (§6.2) — **Sound with caveats**

The Protocol is complete: `run_turn`, `stream`, `interrupt`, `attach`,
`supports_model`, `capabilities` (`harness/base.py:82-90`); `HarnessCapabilities`
carries `cluster_capable` (`base.py:14`), advertised true by the SDK adapter
(`sdk_adapter.py:45`) and false by locals (`local_adapters.py:26`) — and the
exporter/controller gate on exactly that (`agentteam_controller.go:20`). For an M0
*interface* deliverable this is the right shape.

**Caveats (mostly M1 scope, but flagged now):**
- **`attach()` is a stub end-to-end** — `AttachSession.inject`/`.interrupt` raise
  `NotImplementedError` (`base.py:71`); both adapters return a bare session
  (`sdk_adapter.py:111`, `local_adapters.py:43`). The "real interactive seam"
  doesn't exist yet (it's CLI-M1).
- **`AgentContext.memory/mailbox/budget` are never injected** — `_context()` leaves
  them `None` (`coordinator.py:130-139`), so the comment at `base.py:57` ("injected
  by the Coordinator") is currently false. An adapter cannot reach those accessors.
- **No attach-gate field on `AgentContext`** and `Task.attached_by` is written/read
  nowhere (`grep` → dataclass only).
- **No adapter contract test** (the §19/§871 shared suite) — absent.

---

## 1.6 Boundaries & guardrails — **Sound with caveats**

- ✅ **Local runtime is an OS process, not Kind/pods.** No kind/pod logic in `cli/`
  or `karo_runtime/`; the Coordinator drives the SDK adapter in-process
  (`coordinator.py`). The v1 pain point is avoided.
- ✅ **No agent-reasoning logic in the Go controller.** Verified orchestration-only;
  the only domain constant is harness-gating `clusterCapableHarnesses=["sdk"]`
  (`agentteam_controller.go:20`), which is legitimate. `cmd/main.go` documents the
  boundary. Reasoning lives in Python (`harness/`, `runtime/`).
- ✅ **Secrets never baked into images/CRDs.** Export emits `secretRef`
  (`exporter/manifest.py`, `test_export.py:79`); compiler never resolves.
- ⚠️ **The `spec` body is *not* identical CLI ↔ operator in practice** — the Go CRD
  is a lossy subset and prunes `resources`/`memory` (§1.1). The guardrail "keep the
  spec body identical; change `runtime:`, never `spec:`" (§21) is violated on the Go
  side, not by adding to spec but by **dropping from it**.

---

## 1.7 Tests — honest coverage of the M0 surface

**Genuinely tested (T):** pydantic model shape + apiVersion rejection
(`test_spec.py`); folder & flat compile, body→instructions, auto-discovery; canonical
projection / materialized defaults / namespace+runtime exclusion / plain-decimal ints
(`test_spec.py`, `test_export.py`); export secretRef-never-value + round-trip parity
(`test_export.py`); file store CRUD, atomic claim, deps, stale-lease, mailbox
hardLimit, memory gc (`test_stores.py`); budget reserve / no-overspend (single-process)
/ onExceed modes (`test_budget.py`); coordinator dry-run / full-run / pipeline-deps /
resume / supervised-pause / budget-hardstop (`test_coordinator.py`); CLI
init/validate/run/export/schema/mail (`test_cli.py`); Go validate helpers +
scale-to-zero (`agentteam_controller_test.go`).

**Asserted-by-comment / missing (M, U):**
- Adapter **contract** test — **missing** (§19).
- Store **conformance** parametrization — **missing**; file-only (§1.4).
- `include:` deep-merge — **untested**.
- Secret interpolation primitives — **untested** (spec layer).
- Folder-compile == flat-team.yaml round-trip — **missing** (§4.0.1).
- **envtest reconcile** test (apply→Pending, status, objects) — **missing**; Go tests
  are pure unit helpers, which is why the `Running`-instead-of-`Pending` bug survives.
- **Go ⇄ JSON-Schema** drift test — **missing** (the critical one, §1.1).
- e2e on kind — **placeholder** (`t.Skip`, `e2e_test.go:16`).
- SDK real-call path — `# pragma: no cover`, untested (acceptable; needs creds).

Coverage of the *Python file-backed* surface is genuinely good. Coverage of the
*parity surface* (Go↔schema, backend conformance, folder↔flat) — the thing M0 is
for — is the weakest.

---

## 1.8 Skeptical-adopter pass

**Five things a new contributor hits in the first hour:**
1. `karo compile` — the first thing you'd run to see the artifact — **crashes** with
   a raw pydantic traceback (B2). Terrible first impression.
2. Clone + push-to-cluster: `kubectl apply` of your exported team **silently drops
   `resources`/`memory`** (no error), so your MCP servers/skills/tools just vanish on
   the cluster and nothing tells you why (§1.1).
3. Green CI locally is unattainable — the schema drift step fails on a pristine
   checkout (B1), so you can't tell real drift from the bug.
4. You read `stores/base.py` expecting the advertised shared contract test to copy
   for your redis backend; there isn't one — the tests are welded to the file store.
5. `karo attach` (a headline feature in the README/PRD) prints "lands in M1+" and
   exits — discoverable only by running it.

**The single worst smell:** the project's entire thesis — *the same `AgentTeam` runs
locally and on Kubernetes* — has **no enforcement and is already violated by
construction**: the Go CRD is a lossy hand-written subset that prunes the shared
spec on apply, the comment claiming the `preserve-unknown-fields` safety net is
false, and no CI job compares the Go types to the schema. Everything else is fixable
hygiene; this is the foundation crack.

---

## Foundation verdict (rolled up to Phase 5)

| Area | Verdict |
|---|---|
| 1.1 Single source of truth | **Broken** |
| 1.2 Compiled spec model | Sound |
| 1.3 Folder compiler | Sound with caveats |
| 1.4 Store Protocols | Concerning |
| 1.5 HarnessAdapter Protocol | Sound with caveats |
| 1.6 Boundaries & guardrails | Sound with caveats |
| 1.7 Tests | Sound (Python) / Concerning (parity) |

The Python core is a credible foundation. The **parity machinery (1.1) is not yet a
foundation** — it must be fixed before M1 builds weight on it. Fix plan: `02-fixplan.md`.
