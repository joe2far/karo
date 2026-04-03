# Team Builder Team Example

This example deploys a meta-team that researches existing teams, projects, and issue history
to automatically generate production-ready KARO AgentTeam specifications.

## Agents

- **Team Architect** (orchestrator) - Designs optimal agent team compositions based on research findings
- **Research Analyst** (executor) - Investigates Jira issues, GitHub repos, and existing team patterns
- **Spec Generator** (executor) - Produces and validates complete KARO YAML manifests

## How It Works

Given a team name or project identifier, the Team Builder:
1. Research Analyst queries Jira for the last 90 days of issues to identify common workflows
2. Research Analyst analyzes GitHub repos for CI/CD patterns, tech stack, and PR workflows
3. Research Analyst reviews existing KARO AgentTeam specs for reusable patterns
4. Team Architect synthesizes findings into an optimal team design
5. Spec Generator produces complete, validated KARO YAML manifests
6. Team Architect reviews the final output for quality and completeness

## Architecture

```
         ┌────────────────┐
         │  AgentChannel   │ (Slack)
         │  team-builder   │
         └────────┬───────┘
                  │ "build team for project X"
         ┌────────▼───────┐
         │   TaskGraph     │
         │  build-team-*   │
         └────────┬───────┘
                  │
         ┌────────▼───────┐
         │   Dispatcher    │
         │  team-builder   │
         └────────┬───────┘
      ┌───────────┼───────────┐
      ▼           ▼           ▼
  Team         Research     Spec
  Architect    Analyst      Generator
```

## Quick Start

```bash
kubectl create namespace team-builder
kubectl apply -f examples/team-builder-team/
kubectl get taskgraph -n team-builder -w
```

## Output

The team produces a complete set of KARO manifests ready to `kubectl apply`:
- Namespace, Secrets, ModelConfig, SandboxClass, MemoryStore, ToolSet
- AgentPolicy, EvalSuite, AgentSpecs, AgentTeam, Dispatcher
- AgentLoop and AgentChannel (if applicable)
- README with architecture diagram and quick start guide
