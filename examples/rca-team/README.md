# RCA Team Example

This example deploys an RCA (Root Cause Analysis) team that, when prompted with a Slack
incident channel, reviews all content in the channel and creates a detailed RCA Google Doc
from the organization's standard template.

## Agents

- **RCA Lead** (orchestrator) - Plans the investigation, reviews the final document
- **Channel Investigator** (executor) - Reads Slack channel history, extracts events and timeline
- **RCA Author** (executor) - Writes the RCA Google Doc, creates Jira follow-up tickets

## How It Works

When triggered with a Slack incident channel (e.g., `#inc-2026-0401-payment-outage`):
1. RCA Lead reads the channel overview and plans the investigation
2. Channel Investigator reads the full channel history, extracting events and decisions
3. Channel Investigator cross-references with PagerDuty for official incident timeline
4. RCA Author retrieves prior RCA documents from Google Drive for template and pattern matching
5. RCA Author writes the complete RCA Google Doc with timeline, root cause, and follow-ups
6. RCA Author creates Jira tickets for each follow-up action item
7. RCA Lead reviews the document and posts the link to the incident channel

## Architecture

```
  ┌────────────────┐
  │  AgentChannel   │ (Slack: #rca-team)
  │  rca-slack      │
  └────────┬───────┘
           │ "Create RCA for #inc-channel"
  ┌────────▼───────┐
  │   TaskGraph     │
  │  rca-YYYY-*     │
  └────────┬───────┘
           │
  ┌────────▼───────┐
  │   Dispatcher    │
  │  rca-router     │
  └────────┬───────┘
     ┌─────┼─────────┐
     ▼     ▼         ▼
  RCA    Channel    RCA
  Lead   Invest.    Author
```

## RCA Document Template

The team follows the organization's standard RCA template:
- Executive Summary
- Incident Details (duration, services, impact)
- Timeline (with timestamps and sources)
- Root Cause Analysis (proximate cause + contributing factors)
- Impact Assessment
- Mitigation Steps
- Follow-up Actions (immediate, short-term, long-term) with Jira tickets
- Lessons Learned

## Integrations

- **Slack**: Reads incident channel history, posts RCA link when complete
- **PagerDuty**: Cross-references incident timeline and escalation data
- **Google Docs**: Creates and writes the RCA document
- **Jira**: Creates follow-up action item tickets

## Quick Start

```bash
kubectl create namespace rca-team
kubectl apply -f examples/rca-team/

# Trigger an RCA by posting to Slack or creating a TaskGraph:
kubectl apply -f examples/rca-team/05-taskgraph.yaml
kubectl get taskgraph -n rca-team -w
```
