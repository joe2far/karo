# SRE Team Example

This example deploys an SRE (Site Reliability Engineering) team with four specialized agents
for Root Cause Analysis (RCA) and capacity planning:

- **Incident Commander Agent** - Coordinates incident response, creates RCA timelines, writes post-mortems
- **Diagnostics Agent** - Queries metrics, logs, and traces to identify root causes
- **Capacity Planner Agent** - Analyzes resource utilization trends, forecasts capacity needs
- **Remediation Agent** - Creates and validates infrastructure fixes, writes runbooks

## Use Cases

### Root Cause Analysis (RCA)
When an incident occurs, the SRE team:
1. Incident Commander creates a TaskGraph with the incident timeline
2. Diagnostics Agent queries Prometheus, Loki, and Kubernetes events
3. Diagnostics Agent identifies the root cause and contributing factors
4. Remediation Agent creates a fix (infra change, config update, etc.)
5. Incident Commander writes the post-mortem report

### Capacity Planning
On a weekly schedule:
1. Capacity Planner queries resource utilization metrics
2. Analyzes trends and forecasts future needs
3. Identifies services approaching resource limits
4. Recommends scaling actions (HPA tuning, node pool changes, etc.)
5. Creates tickets for proactive scaling

## Architecture

```
         ┌────────────────┐
         │  AgentChannel   │ (PagerDuty/Slack)
         │  sre-pagerduty  │
         └────────┬───────┘
                  │ incident alerts
         ┌────────▼───────┐
         │   TaskGraph     │ (incident RCA / capacity review)
         │  incident-xxx   │
         └────────┬───────┘
                  │
         ┌────────▼───────┐
         │   Dispatcher    │
         │  sre-router     │
         └────────┬───────┘
      ┌───────────┼───────────┬──────────────┐
      ▼           ▼           ▼              ▼
  Incident    Diagnostics  Capacity     Remediation
  Commander    Agent       Planner       Agent
```

## Quick Start

```bash
kubectl create namespace sre-team
kubectl apply -f examples/sre-team/
kubectl get taskgraph -n sre-team -w
```
