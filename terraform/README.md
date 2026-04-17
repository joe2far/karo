# KARO Cloud Deployments

Production-ready OpenTofu configurations for deploying KARO on major cloud platforms.

## Available Deployments

| Directory | Cloud | Cluster | AI Provider | Auth Model |
|-----------|-------|---------|-------------|------------|
| [`gke-autopilot/`](gke-autopilot/) | GCP | GKE Autopilot | Vertex AI (Gemini 4) | Workload Identity |
| [`eks-automode/`](eks-automode/) | AWS | EKS Auto Mode | Amazon Bedrock (Claude) | IRSA |

Both deployments include:
- Fully managed Kubernetes cluster (no node management)
- Cloud-native IAM for model access (no static API keys)
- KARO operator installed via Helm
- Example 3-agent dev team (Planner, Coder, Reviewer) with Claude Code harness
- Quickstart TaskGraph to verify end-to-end flow
- Governance policy, sandbox isolation, and MCP tooling pre-configured

## Quick Comparison

| Feature | GKE Autopilot | EKS Auto Mode |
|---------|---------------|---------------|
| Node management | Google-managed | AWS-managed |
| Sandbox isolation | gVisor (native) | runc (default) |
| AI model auth | Workload Identity (KSA→GSA) | IRSA (OIDC federation) |
| Model | Gemini 4 Flash | Claude Sonnet via Bedrock |
| Agent harness | Claude Code | Claude Code |

## Prerequisites

- [OpenTofu](https://opentofu.org/docs/intro/install/) >= 1.6
- Cloud CLI authenticated (`gcloud` or `aws`)
- `kubectl` installed

## Getting Started

```bash
# Choose your cloud
cd gke-autopilot/   # or eks-automode/

# Configure
cp terraform.tfvars.example terraform.tfvars
# Edit with your values

# Deploy
tofu init && tofu apply

# Connect
$(tofu output -raw kubeconfig_command)

# Verify
kubectl get agentteams -n karo-agents
```
