---
name: approve-deploy
description: >
  Run a pre-deployment checklist against a Jira deployment ticket and, on PASS,
  transition it to "Ready for Deploy" — behind a human approval gate.
---
# approve-deploy

Use this skill when you are given a deployment Jira ticket (e.g. `JIRA-789`) and
asked to approve it for release.

## Inputs

- A Jira ticket ID (the deployment / change-request ticket).
- Optional: a PR number or commit range linked to the ticket.

## Procedure

1. **Read context.** Use the `jira` MCP server to fetch the ticket, its status,
   linked PRs, and any blocking issues.
2. **Run the checklist.** Confirm each item:
   - [ ] Definition of Done met (tests green, docs updated).
   - [ ] No open blocking issues linked to the ticket.
   - [ ] Deployment manifest complies with the org's infra policy.
   - [ ] Rollback plan is documented on the ticket.
3. **Decide.** Emit a verdict:
   - `PASS` — all items satisfied. Proceed to step 4.
   - `FAIL` — list the failing items and stop. Do **not** transition the ticket.
4. **Transition (gated).** On `PASS`, call the Jira transition tool
   (`mcp:jira/transition_issue`) to move the ticket to *Ready for Deploy*. This
   tool is behind a `pauseBefore` guard:
   the agent pauses and waits for a human to release it with
   `karo attach deploy-approver --continue` before the transition is applied.

## Output

A short report: the verdict, the checklist results, and (on PASS, post-release)
the new ticket status.
