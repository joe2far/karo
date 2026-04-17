# KARO on EKS Auto Mode with Amazon Bedrock

Deploy KARO and an example AI agent team on AWS using EKS Auto Mode and Amazon Bedrock.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      AWS Account                            │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │               VPC (10.0.0.0/16)                      │   │
│  │                                                      │   │
│  │  Public Subnets ─── IGW     Private Subnets ─── NAT  │   │
│  │                                                      │   │
│  │  ┌────────────────────────────────────────────────┐  │   │
│  │  │        EKS Auto Mode Cluster                   │  │   │
│  │  │   (compute, storage, networking managed)       │  │   │
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
│                              IRSA (OIDC)                    │
│                                      │                      │
│                    ┌─────────────────▼─────────────────┐    │
│                    │       Amazon Bedrock              │    │
│                    │  (Claude Sonnet via Anthropic)    │    │
│                    └──────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

## What Gets Deployed

**Infrastructure:**
- VPC with public/private subnets across 3 AZs, NAT Gateway, Internet Gateway
- EKS Auto Mode cluster (AWS manages compute node pools, storage, and load balancing)
- OIDC provider for IRSA (IAM Roles for Service Accounts)
- IAM role with `bedrock:InvokeModel` permissions

**KARO Operator:**
- Helm release in `karo-system` namespace
- All 14 CRDs registered

**Example Agent Team** (when `enable_example_agents = true`):
- `ModelConfig` — AWS Bedrock Claude Sonnet (no API keys, uses IRSA)
- `SandboxClass` — runc isolation with restricted egress to Bedrock endpoints
- `ToolSet` — GitHub MCP + code executor
- `AgentPolicy` — governance: audit, data classification, tool limits
- 3 `AgentSpec` resources: Planner (orchestrator), Coder (executor), Reviewer (evaluator)
- `Dispatcher` — capability-based task routing
- `AgentTeam` — binds all agents with shared resources
- `TaskGraph` — quickstart hello-world task to verify end-to-end flow

## Prerequisites

- [OpenTofu](https://opentofu.org/docs/intro/install/) >= 1.6
- [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) configured
- AWS account with Bedrock model access enabled
- Request access to your chosen Bedrock model in the AWS Console

## Quick Start

```bash
# 1. Clone and navigate
cd terraform/eks-automode

# 2. Configure
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your region and credentials

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

This deployment uses **IRSA (IAM Roles for Service Accounts)** — no static AWS credentials:

1. An OIDC provider is created from the EKS cluster's identity issuer
2. An IAM role (`karo-agent-bedrock`) trusts the OIDC provider for the `karo-agent` SA
3. The IAM role has `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream`
4. Agent pods running as `karo-agent` SA automatically receive temporary AWS credentials

## EKS Auto Mode

EKS Auto Mode simplifies cluster operations by letting AWS manage:

- **Compute**: Node pools (`general-purpose`, `system`) are auto-provisioned and scaled
- **Storage**: EBS CSI driver is built-in via `block_storage`
- **Networking**: Load balancer controller is built-in via `elastic_load_balancing`

No managed node groups or Karpenter configuration needed.

## Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `region` | AWS region | `us-east-1` |
| `cluster_name` | EKS cluster name | `karo-cluster` |
| `cluster_version` | Kubernetes version | `1.32` |
| `bedrock_model_id` | Bedrock model ID | `anthropic.claude-sonnet-4-20250514-v1:0` |
| `enable_example_agents` | Deploy example team | `true` |
| `github_token` | GitHub token for agents | `""` |
| `mem0_api_key` | mem0 API key | `""` |

## Cleanup

```bash
tofu destroy
```
