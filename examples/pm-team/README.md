# pm-team — a Jira-integrated delivery team (local folder format)

A complete, runnable KARO team that mirrors the KARO v1 "PM team" tutorial, but
in the **v2 local folder format**. It runs entirely offline in stub mode (no API
keys, no Jira account needed) and graduates unchanged to Kubernetes via
`karo export`.

The headline scenario: **a `deploy-approver` agent that has the Jira MCP server
and an `approve-deploy` skill, which you fire at a single deployment ticket — with
a human gate before it actually moves the ticket.**

## The team

| Agent | Harness / model | Tools | Autonomy | Role |
|---|---|---|---|---|
| `pm-lead` | sdk / opus | — | autonomous | Lead. Decomposes objectives, delegates, synthesizes a status update. |
| `cr-reviewer` | sdk / sonnet | `jira` MCP | autonomous | Reviews PRs / Jira tickets, posts a verdict. |
| `deploy-approver` | sdk / opus | `jira` MCP + `approve-deploy` skill | **supervised** | Runs the pre-deploy checklist and transitions the ticket — **behind a human gate**. |

```
examples/pm-team/
  karo.yaml                         # lead-and-teammates, lead: pm-lead
  agents/
    pm-lead/AGENT.md
    cr-reviewer/AGENT.md
    deploy-approver/AGENT.md        # mcp:[jira], skills:[approve-deploy], pauseBefore guard
  skills/
    approve-deploy/SKILL.md         # the pre-deploy checklist + gated transition
  mcp/servers.yaml                  # the jira MCP server (creds via ${env:…})
```

## 1. Validate

```bash
cd examples/pm-team
karo validate
# → ok: no issues found
```

## 2. Sling a deploy-approval at one agent

This is the "fire a specific comment at a specific agent" flow. The objective is
your comment; `--agent` is the target:

```bash
karo run --agent deploy-approver \
  -o "Approve deploy for JIRA-789: ship auth-service v2.3"
```

`deploy-approver` runs the `approve-deploy` skill and then **pauses at the human
gate** — the Jira transition (`mcp:jira/transition_issue`) is behind a
`pauseBefore` guard:

```
· task.transition  deploy-approver  task_id=task-6e09… from_state=(new) to_state=pending
· task.transition  deploy-approver  task_id=task-6e09… from_state=assigned to_state=paused
· guard.pause      deploy-approver  guard=pauseBefore:mcp:jira/transition_issue
run …: incomplete
  paused (attach to steer): deploy-approver
```

## 3. Approve at the gate, then let it finish

```bash
karo ps
# deploy-approver  paused:guard   task-6e09…

karo attach deploy-approver --continue
# released 1 paused task(s) for deploy-approver

# re-run — the released task is reused and proceeds past the gate
karo run --agent deploy-approver \
  -o "Approve deploy for JIRA-789: ship auth-service v2.3"
# → run …: completed
```

If the pre-flight check had failed, the agent would stop and report instead of
transitioning the ticket — the gate only matters on `PASS`.

## 4. Or run the whole team

Hand the release to the lead and let it coordinate. The autonomous `cr-reviewer`
completes its review; the supervised `deploy-approver` still stops at its gate
for you:

```bash
karo run -o "Review and approve the JIRA-789 auth-service v2.3 release"
# - task-… [done]   owner=cr-reviewer
# - task-… [paused] owner=deploy-approver   ← attach --continue to release
# - task-… [pending] owner=pm-lead
```

## 5. Go live (real model + real Jira)

Stub mode is for learning the workflow. To run for real:

1. Install the SDK harness and set Anthropic credentials:
   ```bash
   pip install 'karo-runtime[sdk]'
   export ANTHROPIC_API_KEY=…
   ```
2. Provide Jira credentials for the MCP server (referenced in `mcp/servers.yaml`):
   ```bash
   export JIRA_URL=https://your-org.atlassian.net
   export JIRA_EMAIL=you@your-org.com
   export JIRA_API_TOKEN=…          # or: karo secret set JIRA_API_TOKEN …
   ```
   ...and make sure a `jira-mcp-server` binary is on your PATH (swap the
   `command:` in `mcp/servers.yaml` for whichever Jira MCP server you use).
3. Run exactly the same commands as above — the spec doesn't change.

## 6. Hand off to Kubernetes

```bash
karo export -o pm-team.manifest.yaml --namespace pm-team
kubectl apply -f pm-team.manifest.yaml     # requires the KARO v2 operator
```

The `permissionMode: prompt` on `deploy-approver` has no interactive surface on a
cluster, so `karo export` coerces it into an equivalent `pauseBefore` guard —
`karo validate --target cluster` warns you about this up front.
