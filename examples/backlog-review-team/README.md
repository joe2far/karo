# Backlog Review Team Example

A team of agents that runs a weekly backlog review for one or more engineering
teams. Each subject team (team-a, team-b, team-c) has its own JQL query, its
own cron schedule, and its own Slack channel. The agents pull tickets via a
Jira MCP server, score them against a shared story/epic template and
refinement process, and post a summary back to the team's Slack channel via a
Slack MCP server.

## Agents

- **Backlog Lead** (orchestrator) - plans the review, composes the weekly
  summary, decides what to post to Slack, records snapshots for week-over-week
  deltas.
- **Refinement Analyst** (executor) - scores each ticket against the story /
  epic template and the refinement process; posts Jira comments listing gaps.
- **Story Writer** (executor) - drafts missing template sections (user-story
  sentence, acceptance criteria, rollout plan) as Jira comments for the ticket
  owner to accept.

## MCP Servers Used

The agents talk to three MCP servers, all referenced from the `backlog-tools`
ToolSet:

| MCP server              | Transport         | Purpose                                            |
| ----------------------- | ----------------- | -------------------------------------------------- |
| `jira`                  | streamable-http   | JQL search, issue read, issue comment (no body edits) |
| `slack`                 | streamable-http   | Post weekly summaries to the team's channel         |
| `refinement-templates`  | streamable-http   | Serves the story template, epic template, refinement process, and per-team JQL |

The `refinement-templates` server is a small in-cluster service that mounts
the ConfigMaps in `03-configmaps.yaml` and serves them as MCP resources. Its
deployment is intentionally out of scope for this example (you point the
endpoint at your own implementation).

## Per-Team Configuration

Three things are team-specific: the JQL query, the Slack channel, and the
cron schedule. Everything else (agents, templates, refinement process,
sandbox, policy) is shared.

| Subject team | Project | Cron (UTC)   | Slack channel       | Top-N scored |
| ------------ | ------- | ------------ | ------------------- | ------------ |
| team-a       | PLAT    | Mon 08:00    | `C0TEAM-A-BACKLOG`  | 15           |
| team-b       | PAY     | Tue 08:00    | `C0TEAM-B-BACKLOG`  | 20           |
| team-c       | GRW     | Wed 08:00    | `C0TEAM-C-BACKLOG`  | 12           |

Per-team JQL lives in `03-configmaps.yaml` (one ConfigMap per team). Per-team
cron + Slack channel + top-N lives in the `loopPrompt` of each per-team
`AgentLoop` in `07-loops-channels.yaml`. Adding a fourth team is a matter of
adding one ConfigMap, one AgentLoop, and one AgentChannel - no agent or
policy changes required.

## Refinement Process

The refinement process is a six-stage checklist (Triage -> Shape -> Slice ->
Dependencies -> DoD -> Ready for Dev). The full process, along with the
story and epic templates the agents score against, lives in the three shared
ConfigMaps:

- `story-template`
- `epic-template`
- `refinement-process`

These are authored in Markdown and deliberately kept human-editable - a
product manager can update the template or the process playbook without
touching any agent configuration.

## Architecture

```
  ┌──────────────────────┐      ┌──────────────────────┐      ┌──────────────────────┐
  │  AgentLoop           │      │  AgentLoop           │      │  AgentLoop           │
  │  weekly-review-team-a│      │  weekly-review-team-b│      │  weekly-review-team-c│
  │  cron: Mon 08:00     │      │  cron: Tue 08:00     │      │  cron: Wed 08:00     │
  └──────────┬───────────┘      └──────────┬───────────┘      └──────────┬───────────┘
             │                              │                              │
             ▼                              ▼                              ▼
                       ┌─────────────────────────────────────┐
                       │  Backlog Lead (orchestrator)        │
                       │  - plans TaskGraph per run          │
                       │  - composes weekly summary          │
                       └──────────────────┬──────────────────┘
                                          │
                                          ▼
                       ┌─────────────────────────────────────┐
                       │  Dispatcher (capability routing)    │
                       └─────┬────────────────────────┬──────┘
                             │                        │
                             ▼                        ▼
              ┌──────────────────────┐   ┌──────────────────────┐
              │ Refinement Analyst   │   │ Story Writer         │
              │ (per-ticket scoring) │   │ (drafts missing bits)│
              └──────────┬───────────┘   └──────────┬───────────┘
                         │                          │
                         ▼                          ▼
                  ┌───────────────────────────────────────┐
                  │  MCP tools: jira, slack, refinement-  │
                  │  templates                            │
                  └───────────────────────────────────────┘
                                    │
                                    ▼
                 #team-a-backlog   #team-b-backlog   #team-c-backlog
```

## Files

| File                            | Contents                                                  |
| ------------------------------- | --------------------------------------------------------- |
| `00-namespace.yaml`             | Namespace                                                 |
| `01-secrets.yaml`               | Anthropic, Jira, Slack, Slack signing, mem0 credentials   |
| `02-infrastructure.yaml`        | ModelConfig, SandboxClass, MemoryStore, ToolSet, Policy, EvalSuite |
| `03-configmaps.yaml`            | Per-team JQL (x3) + story / epic / refinement templates   |
| `04-agents.yaml`                | AgentSpecs: backlog-lead, refinement-analyst, story-writer|
| `05-team.yaml`                  | AgentTeam + Dispatcher                                    |
| `06-taskgraph-template.yaml`    | Example concrete TaskGraph for a single weekly run        |
| `07-loops-channels.yaml`        | Per-team AgentLoops (x3) + per-team AgentChannels (x3)    |

## Quick Start

```bash
kubectl apply -f examples/backlog-review-team/

# Watch the per-team loops fire on their schedule.
kubectl get agentloop -n backlog-review-team

# Trigger a dry run against team-a without waiting for Monday:
kubectl apply -f examples/backlog-review-team/06-taskgraph-template.yaml
kubectl get taskgraph -n backlog-review-team -w
```

Before applying:

1. Fill in the `<your-...>` placeholders in `01-secrets.yaml`.
2. Replace the Slack channel IDs (`C0TEAM-A-BACKLOG`, etc.) in
   `03-configmaps.yaml` and `07-loops-channels.yaml` with your real channel
   IDs.
3. Replace the JQL in `03-configmaps.yaml` to match your Jira project keys
   and team-dropdown field names.
4. Deploy a Jira MCP server, a Slack MCP server, and a refinement-templates
   MCP server at the endpoints referenced in `02-infrastructure.yaml`.

## Extending to More Teams

1. Add a `<team>-jql` ConfigMap to `03-configmaps.yaml`.
2. Add a new `AgentLoop` in `07-loops-channels.yaml` with the team's cron,
   loop prompt (team key, JQL ref, channel ID, top-N), and eval gate.
3. Add a new `AgentChannel` with the team's Slack channel ID.
4. Add the new memory category name to the `MemoryStore` `categories` list
   in `02-infrastructure.yaml`.

No changes are required to agents, dispatcher, policy, templates, or the
refinement process.
