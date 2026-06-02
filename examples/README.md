# KARO examples

Complete, runnable agent teams in the **local folder format** (`karo.yaml` +
`agents/` + `skills/` + `mcp/`). Each one validates and runs offline in stub
mode (no API keys) and graduates to Kubernetes unchanged via `karo export`.

| Example | What it shows |
|---|---|
| [`pm-team/`](pm-team/) | A Jira-integrated delivery team. The flagship: a `deploy-approver` agent with the **Jira MCP server** + an **`approve-deploy` skill**, fired at a single ticket with `karo run --agent`, gated by a human approval (`pauseBefore` + `karo attach --continue`). Mirrors the KARO v1 PM-team tutorial. |

To try one:

```bash
cd examples/pm-team
karo validate
karo run --agent deploy-approver -o "Approve deploy for JIRA-789"
```

For the concepts behind these (the run model, slinging to a single agent,
guards and the human gate, secrets, export), see [`docs/USAGE.md`](../docs/USAGE.md).
For a full developer journey — scaffold a dev team, sling a feature from a file,
**open a PR**, share it, and graduate to Kubernetes with a git **service
account** — see [`docs/DEV-WORKFLOW.md`](../docs/DEV-WORKFLOW.md).

These examples carry **no org-specific identifiers** and are safe to copy as a
starting point for your own teams.
