# KARO Implementation Prompt for Claude Code

## Instructions

You are implementing **KARO (Kubernetes Agent Runtime Orchestrator)** — a Kubernetes-native operator that provides first-class CRD primitives for deploying, orchestrating, and governing AI agents at scale.

The complete specification is in `karo-spec-v0.4.0-alpha.md` in this repository. **Read the entire spec before writing any code.** The spec is the single source of truth — it contains all CRD definitions, Go type definitions, controller reconciliation flows, implementation order, and code examples. Do not deviate from it.

## Project Setup

1. **Read the spec first.** Open and thoroughly read `karo-spec-v0.4.0-alpha.md`. It is ~5,200 lines and contains everything you need:
   - 14 CRD definitions with full YAML examples and Go type definitions (96 types total)
   - Controller reconciliation flow diagrams for every controller
   - Implementation code examples (reconcilers, DAG walking, eval gate runner, message delivery, mutation validation)
   - Project structure with exact file paths
   - Implementation order (15 steps, dependency-ordered)
   - Scaffold commands for operator-sdk

2. **Scaffold the operator** using the exact commands in "Step 1: Scaffold the Operator" in the spec. Use:
   - `operator-sdk init --domain karo.dev --repo github.com/karo-dev/karo --plugins go/v4`
   - Then all 14 `operator-sdk create api` commands
   - Then the webhook scaffold commands

3. **Follow the implementation order** in "Step 2: Implementation Order" exactly:
   ```
   1.  SandboxClass       → 2.  ModelConfig        → 3.  MemoryStore
   4.  ToolSet            → 5.  AgentPolicy        → 6.  EvalSuite
   7.  AgentSpec          → 8.  AgentMailbox       → 9.  AgentInstance
   10. TaskGraph          → 11. Dispatcher         → 12. AgentTeam
   13. AgentLoop          → 14. AgentChannel       → 15. agent-runtime-mcp
   ```

## Implementation Rules

### For each CRD (steps 1-14):

1. **Types file** (`api/v1alpha1/<kind>_types.go`): Copy the Go type definitions exactly from the spec. Every struct, every const, every enum is defined. Add kubebuilder markers for validation, defaults, and the status subresource where applicable.

2. **Controller** (`internal/controller/<kind>_controller.go`): Implement the reconciliation logic as described in the "Controller responsibilities" section for that CRD. The spec includes Mermaid flow diagrams and Go code examples for key controllers (TaskGraph, Dispatcher, AgentMailbox, AgentInstance).

3. **Sample manifest** (`config/samples/<kind>-sample.yaml`): Create from the YAML examples in the spec.

4. **Tests**: Write unit tests for each controller using envtest. At minimum test the happy path reconciliation and key error cases.

### Key Implementation Patterns (from the spec):

- **TaskGraph spec/status split**: Task definitions in `spec.tasks[]`, all runtime state in `status.taskStatuses` map. Use `/status` subresource. See `seedTaskStatuses`, `reconcileTaskDeps`, `recomputeAggregates`, `runEvalGate` code in the spec.

- **Dispatcher ↔ TaskGraph**: Event-based coupling. TaskGraph emits events, Dispatcher watches via `Watches()`. See `SetupWithManager` and `findDispatcherForTaskGraph` code in the spec.

- **AgentMailbox**: Messages in `status.pendingMessages`, aggressive GC on acknowledgement, `maxPendingMessages: 100` default. See `deliverTask` and `acknowledgeMessage` code in the spec.

- **AgentTeam shared resources**: Resolution at AgentInstance creation time via `resolveEffectiveBindings()`. AgentTeam controller does NOT mutate AgentSpec objects.

- **Agent scaling**: `scaling.startPolicy: OnDemand` (default) means no pod until mailbox message. Dispatcher creates AgentInstances up to `maxInstances`. See updated AgentInstance reconciliation flow.

- **AgentChannel**: Handles approval tasks (`type: approval`) — posts to Slack/Telegram/Discord, waits for human response, closes or fails the task. Multi-team handoff via `teamHandoff` rules.

- **Validation webhooks**: DAG cycle detection (Kahn's algorithm — code provided), mutation permission checks, platform credential validation.

### For the agent-runtime-mcp sidecar (step 15):

This is a separate binary in `cmd/agent-runtime-mcp/main.go`. It is an MCP server (stdio transport) that exposes 8 tools. The spec contains full JSON schemas for every tool in the "MCP Tool Definitions" section:
- `karo_poll_mailbox`, `karo_ack_message`
- `karo_complete_task`, `karo_fail_task`, `karo_add_task`
- `karo_query_memory`, `karo_store_memory`
- `karo_report_status`

The sidecar uses a Kubernetes client (via pod ServiceAccount) to read/write CRDs. Implement in `internal/runtime/`.

### For the harness bootstrap scripts (in `harness/`):

Create the Dockerfiles and bootstrap scripts for both harnesses as described in the "Reference Agent Harnesses" section:
- `harness/goose/` — Goose harness with recipe templates
- `harness/claude-code/` — Claude Code harness with `.mcp.json` template

### For observability:

Create the VMServiceScrape, VMPodScrape, VMRule, and Grafana dashboard ConfigMap as described in the "Observability" section. Include them in the Helm chart templates. Register Prometheus metrics in each controller using controller-runtime's metrics package.

### For the Helm chart:

Create the chart in `charts/karo/` with:
- CRD templates (all 14)
- Operator deployment
- RBAC (ClusterRole, ClusterRoleBinding, ServiceAccount) — see "Step 4: RBAC Requirements"
- Webhook configuration
- Observability resources (VMServiceScrape, VMRule, Grafana dashboard)
- `values.yaml` as defined in the spec

## Quality Standards

- All Go code must compile and pass `go vet` and `golangci-lint`
- Every CRD must have generated deepcopy functions (`make generate`)
- Every CRD must have generated manifests (`make manifests`)
- Unit tests for all controllers using envtest
- All sample manifests must be valid YAML that passes webhook validation
- Helm chart must template correctly (`helm template` should succeed)
- The operator must start successfully in a kind cluster with all CRDs registered

## What NOT to do

- Do not invent new CRDs or fields not in the spec
- Do not skip the status subresource on TaskGraph — the spec/status split is critical
- Do not store task runtime state in `spec.tasks[]` — it lives in `status.taskStatuses`
- Do not have the AgentTeam controller mutate AgentSpec objects
- Do not create Pods directly from the Dispatcher — create AgentInstance CRDs (the AgentInstance controller creates Pods)
- Do not hardcode model provider logic in AgentSpec — it lives in ModelConfig
- Do not implement agent reasoning logic — KARO is the orchestration layer, not the agent framework

## Getting Started

```bash
# Read the spec
cat karo-spec-v0.4.0-alpha.md

# Then scaffold
mkdir -p /tmp/karo-build && cd /tmp/karo-build
operator-sdk init --domain karo.dev --repo github.com/karo-dev/karo --plugins go/v4

# Create all 14 APIs (commands are in the spec)
# Then implement in order: SandboxClass first, agent-runtime-mcp last
```

Begin by reading the spec, then scaffold, then implement step by step. After each CRD, run `make generate && make manifests && go build ./...` to verify compilation before moving to the next.
