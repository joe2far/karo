# Incident Review Team Example

This example deploys a cron-triggered team that runs every Monday morning to review all
incident issues from the past week in Jira, perform detailed analysis, and publish a
weekly summary report.

## Agents

- **Review Coordinator** (orchestrator) - Plans the weekly review, compiles findings, distributes the report
- **Incident Analyst** (executor) - Performs detailed analysis of each individual incident issue
- **Report Writer** (executor) - Compiles analyses into a weekly report and publishes to Confluence

## How It Works

Every Monday at 9:00 AM UTC, the AgentLoop triggers the review:
1. Review Coordinator queries Jira for all incidents from the past 7 days
2. Incident Analyst analyzes each incident (root cause, impact, timeline, follow-ups)
3. Incident Analyst adds analysis summaries as comments on each Jira issue
4. Report Writer checks status of follow-up items from prior weeks
5. Report Writer compiles the weekly report with trends and recommendations
6. Review Coordinator reviews and posts the summary to Slack

## Architecture

```
  ┌─────────────┐
  │  AgentLoop   │ (cron: Monday 9am)
  │  weekly-review│
  └──────┬──────┘
         │ triggers
  ┌──────▼──────┐
  │  TaskGraph   │ (weekly-review-2026-wXX)
  └──────┬──────┘
         │
  ┌──────▼──────┐
  │  Dispatcher  │
  │  incident-   │
  │  review-router│
  └──────┬──────┘
    ┌────┼────────────┐
    ▼    ▼            ▼
 Review  Incident   Report
 Coord.  Analyst    Writer
         (x1-5)
```

## Outputs

- **Jira comments**: Each incident issue gets an analysis summary comment
- **Confluence report**: Weekly summary published to the Incident Reviews space
- **Slack notification**: Summary posted to the `#incident-review` channel
- **Follow-up tracker**: Outstanding items tracked across weeks

## Quick Start

```bash
kubectl create namespace incident-review
kubectl apply -f examples/incident-review-team/
# The team will auto-trigger every Monday at 9am UTC
# To trigger an ad-hoc review, create a TaskGraph manually:
kubectl apply -f examples/incident-review-team/05-taskgraph.yaml
```
