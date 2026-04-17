# KARO on GKE Autopilot with Vertex AI

Deploy KARO and an example AI agent team on Google Cloud using GKE Autopilot and Vertex AI Gemini 4.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     GCP Project                             │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                  VPC (karo-vpc)                       │   │
│  │                                                      │   │
│  │  ┌────────────────────────────────────────────────┐  │   │
│  │  │         GKE Autopilot Cluster                  │  │   │
│  │  │                                                │  │   │
│  │  │  ┌─────────────┐    ┌──────────────────────┐  │  │   │
│  │  │  │ karo-system │    │    karo-agents        │  │  │   │
│  │  │  │             │    │                       │  │  │   │
│  │  │  │ KARO        │    │ Planner  (Claude Code)│  │  │   │
│  │  │  │ Operator    │    │ Coder    (Claude Code)│  │  │   │
│  │  │  │             │    │ Reviewer (Claude Code)│  │  │   │
│  │  │  └─────────────┘    └──────────┬───────────┘  │  │   │
│  │  │                                │              │  │   │
│  │  └────────────────────────────────┼──────────────┘  │   │
│  └───────────────────────────────────┼──────────────────┘   │
│                                      │                      │
│                          Workload Identity                  │
│                                      │                      │
│                    ┌─────────────────▼─────────────────┐    │
│                    │        Vertex AI API              │    │
│                    │     (Gemini 4 Flash)              │    │
│                    └──────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

## What Gets Deployed

**Infrastructure:**
- VPC with private subnet, Cloud NAT, and secondary ranges for pods/services
- GKE Autopilot cluster with Workload Identity enabled
- GCP Service Account with `roles/aiplatform.user` for Vertex AI
- Workload Identity binding (KSA → GSA)

**KARO Operator:**
- Helm release in `karo-system` namespace
- All 14 CRDs registered

**Example Agent Team** (when `enable_example_agents = true`):
- `ModelConfig` — Vertex AI Gemini 4 (no API keys, uses Workload Identity)
- `SandboxClass` — gVisor isolation with restricted egress
- `ToolSet` — GitHub MCP + code executor
- `AgentPolicy` — governance: audit, data classification, tool limits
- 3 `AgentSpec` resources: Planner (orchestrator), Coder (executor), Reviewer (evaluator)
- `Dispatcher` — capability-based task routing
- `AgentTeam` — binds all agents with shared resources
- `TaskGraph` — quickstart hello-world task to verify end-to-end flow

## Prerequisites

- [OpenTofu](https://opentofu.org/docs/intro/install/) >= 1.6
- [gcloud CLI](https://cloud.google.com/sdk/docs/install) authenticated
- GCP project with billing enabled
- Vertex AI API enabled (or let OpenTofu enable it)

## Quick Start

```bash
# 1. Clone and navigate
cd terraform/gke-autopilot

# 2. Configure
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your project_id and credentials

# 3. Deploy
tofu init
tofu plan
tofu apply

# 4. Connect to the cluster
$(tofu output -raw kubeconfig_command)

# 5. Verify KARO is running
kubectl get pods -n karo-system
kubectl get agentteams -n karo-agents
kubectl get taskgraphs -n karo-agents
```

## Authentication Model

This deployment uses **Workload Identity** — no static API keys for Vertex AI:

1. A GCP Service Account (`karo-agent-vertex`) is created with `roles/aiplatform.user`
2. A Kubernetes ServiceAccount (`karo-agent`) is annotated with the GSA email
3. Agent pods running as `karo-agent` SA automatically get Vertex AI credentials via GKE metadata server

## Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `project_id` | GCP project ID | (required) |
| `region` | GCP region | `us-central1` |
| `cluster_name` | GKE cluster name | `karo-cluster` |
| `vertex_ai_model` | Vertex AI model | `gemini-4.0-flash` |
| `enable_example_agents` | Deploy example team | `true` |
| `github_token` | GitHub token for agents | `""` |
| `mem0_api_key` | mem0 API key | `""` |

## Cleanup

```bash
tofu destroy
```
