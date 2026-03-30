# Dev Team Example

This example deploys a full-stack development team with four specialized agents:

- **Planner Agent** - Designs architecture, creates task plans, and manages the TaskGraph
- **Coder Agent** - Implements features, writes code, and fixes bugs
- **Reviewer Agent** - Reviews code for quality, security, and correctness
- **Test Agent** - Writes and runs tests, validates acceptance criteria

## Architecture

```
         ┌──────────────┐
         │  AgentChannel │ (Slack integration)
         │  dev-slack    │
         └──────┬───────┘
                │ approval tasks
         ┌──────▼───────┐
         │   TaskGraph   │ (DAG of tasks)
         │ feature-auth  │
         └──────┬───────┘
                │
         ┌──────▼───────┐
         │  Dispatcher   │ (capability routing)
         │  dev-router   │
         └──────┬───────┘
       ┌────────┼────────┬──────────┐
       ▼        ▼        ▼          ▼
   Planner   Coder   Reviewer    Test
   Agent     Agent    Agent      Agent
```

## Quick Start

```bash
# Create the namespace
kubectl create namespace dev-team

# Apply all resources
kubectl apply -f examples/dev-team/

# Watch the TaskGraph progress
kubectl get taskgraph -n dev-team -w

# Check agent instances
kubectl get agentinstances -n dev-team
```

## Resources Created

| Resource | Name | Purpose |
|----------|------|---------|
| Namespace | `dev-team` | Isolated environment |
| ModelConfig | `claude-sonnet` | Anthropic Claude Sonnet model binding |
| SandboxClass | `dev-sandbox` | gVisor isolation with GitHub/npm/PyPI egress |
| MemoryStore | `dev-team-memory` | Shared team memory (mem0 backend) |
| ToolSet | `dev-tools` | GitHub, code executor, web search tools |
| AgentPolicy | `dev-policy` | Governance rules for all agents |
| EvalSuite | `dev-evals` | Quality gates for code tasks |
| AgentSpec (x4) | planner, coder, reviewer, test | Agent definitions |
| AgentTeam | `dev-team` | Team binding with shared resources |
| Dispatcher | `dev-router` | Capability-based task routing |
| TaskGraph | `feature-auth` | Example OAuth2 feature implementation |
| AgentLoop | `daily-standup` | Daily standup loop trigger |
| AgentChannel | `dev-slack` | Slack integration for approvals |
