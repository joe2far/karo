# Research Team Example

This example deploys an AI research team with three specialized agents:

- **Research Lead Agent** - Plans research investigations, synthesizes findings, writes reports
- **Literature Agent** - Searches academic papers, reads documentation, extracts key findings
- **Analyst Agent** - Performs data analysis, creates experiments, validates hypotheses

## Use Cases

- Literature reviews and competitive analysis
- Technical due diligence on new technologies
- Architecture research and recommendations
- Security vulnerability research
- Performance benchmarking studies

## Architecture

```
         ┌───────────────┐
         │  AgentChannel  │ (Slack for review requests)
         │ research-slack │
         └───────┬───────┘
                 │
         ┌───────▼───────┐
         │   TaskGraph    │ (research investigation DAG)
         │ tech-research  │
         └───────┬───────┘
                 │
         ┌───────▼───────┐
         │   Dispatcher   │ (capability routing)
         │ research-router│
         └───────┬───────┘
        ┌────────┼────────┐
        ▼        ▼        ▼
    Research  Literature  Analyst
     Lead      Agent      Agent
```

## Quick Start

```bash
kubectl create namespace research-team
kubectl apply -f examples/research-team/
kubectl get taskgraph -n research-team -w
```
