# KARO GKE Deployment Guide

Deploy the KARO operator and the **dev-team** example on Google Kubernetes Engine.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [GKE Cluster Setup](#2-gke-cluster-setup)
3. [Install Cluster Dependencies](#3-install-cluster-dependencies)
4. [Build & Push the Operator Image](#4-build--push-the-operator-image)
5. [Build & Push Agent Harness Images](#5-build--push-agent-harness-images)
6. [Install the KARO Operator](#6-install-the-karo-operator)
7. [Configure Secrets for the Dev Team](#7-configure-secrets-for-the-dev-team)
8. [Deploy the Dev Team Example](#8-deploy-the-dev-team-example)
9. [Verify the Deployment](#9-verify-the-deployment)
10. [Using Vertex AI (No API Keys)](#10-using-vertex-ai-no-api-keys)
11. [Observability Setup](#11-observability-setup)
12. [Troubleshooting](#12-troubleshooting)
13. [Cleanup](#13-cleanup)

---

## 1. Prerequisites

### Tools

| Tool | Minimum Version | Install |
|------|----------------|---------|
| `gcloud` | latest | [Install Guide](https://cloud.google.com/sdk/docs/install) |
| `kubectl` | 1.28+ | `gcloud components install kubectl` |
| `helm` | 3.12+ | [Install Guide](https://helm.sh/docs/intro/install/) |
| `docker` | 24+ | [Install Guide](https://docs.docker.com/get-docker/) |

### GCP APIs

Enable the required APIs in your project:

```bash
export GCP_PROJECT="<your-gcp-project-id>"
gcloud config set project $GCP_PROJECT

gcloud services enable \
  container.googleapis.com \
  artifactregistry.googleapis.com \
  iam.googleapis.com \
  compute.googleapis.com
```

### API Keys / Tokens (for the dev-team example)

You will need the following credentials ready:

| Credential | Used By | How to Get |
|-----------|---------|------------|
| Anthropic API key | `ModelConfig` (claude-sonnet) | [console.anthropic.com](https://console.anthropic.com/) |
| GitHub personal access token | `ToolSet` + coder agent git push | GitHub Settings > Developer settings > Fine-grained tokens |
| mem0 API key | `MemoryStore` (shared team memory) | [app.mem0.ai](https://app.mem0.ai/) |
| Slack bot token + signing secret | `AgentChannel` (approvals) | [api.slack.com/apps](https://api.slack.com/apps) |

> **Tip:** For a minimal test, you only need the Anthropic API key. The mem0, GitHub, and Slack integrations can be stubbed out or skipped initially.

---

## 2. GKE Cluster Setup

### Create an Artifact Registry repository (for images)

```bash
export REGION="us-central1"
export AR_REPO="karo"

gcloud artifacts repositories create $AR_REPO \
  --repository-format=docker \
  --location=$REGION \
  --description="KARO operator and harness images"
```

### Configure Docker to authenticate with Artifact Registry

```bash
gcloud auth configure-docker ${REGION}-docker.pkg.dev
```

### Create the GKE cluster

```bash
export CLUSTER_NAME="karo-dev"

gcloud container clusters create $CLUSTER_NAME \
  --region $REGION \
  --num-nodes 2 \
  --machine-type e2-standard-4 \
  --disk-size 100 \
  --enable-autoscaling --min-nodes 1 --max-nodes 6 \
  --workload-pool=${GCP_PROJECT}.svc.id.goog \
  --release-channel regular \
  --enable-ip-alias
```

Key flags explained:

| Flag | Why |
|------|-----|
| `--machine-type e2-standard-4` | 4 vCPU / 16 GB — enough for operator + 2-3 concurrent agent pods |
| `--enable-autoscaling` | Agent pods are ephemeral and bursty; autoscaling handles demand spikes |
| `--max-nodes 6` | Ceiling for cost control; adjust based on `maxInstances` across all agents |
| `--workload-pool` | Enables Workload Identity (required for Vertex AI, optional otherwise) |

### Get credentials

```bash
gcloud container clusters get-credentials $CLUSTER_NAME --region $REGION
kubectl cluster-info
```

### (Optional) Install gVisor runtime class

The `dev-sandbox` SandboxClass in the dev-team example requires `runtimeClassName: gvisor`. On GKE, enable it with a **sandbox node pool**:

```bash
gcloud container node-pools create sandbox-pool \
  --cluster=$CLUSTER_NAME \
  --region=$REGION \
  --machine-type e2-standard-4 \
  --num-nodes 1 \
  --enable-autoscaling --min-nodes 0 --max-nodes 4 \
  --sandbox type=gvisor
```

If you skip this, edit `02-infrastructure.yaml` and remove or comment out the `runtimeClassName: gvisor` line from the SandboxClass before deploying.

---

## 3. Install Cluster Dependencies

### cert-manager (required for webhooks)

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update

helm install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --version v1.14.5 \
  --set crds.enabled=true

# Verify
kubectl get pods -n cert-manager
```

Wait until all cert-manager pods are `Running` before proceeding.

### (Optional) VictoriaMetrics or Prometheus stack

The KARO Helm chart includes `VMServiceScrape`, `VMPodScrape`, and `VMRule` resources. These require either [VictoriaMetrics Operator](https://docs.victoriametrics.com/operator/) or [Prometheus Operator](https://prometheus-operator.dev/) to be installed.

For a quick dev setup with VictoriaMetrics:

```bash
helm repo add vm https://victoriametrics.github.io/helm-charts
helm repo update

helm install victoria-metrics-operator vm/victoria-metrics-operator \
  --namespace monitoring \
  --create-namespace
```

Or with the kube-prometheus-stack:

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm install kube-prometheus prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --create-namespace
```

If you skip this, set `observability.vmServiceScrape.enabled=false`, `observability.vmPodScrape.enabled=false`, and `observability.vmRule.enabled=false` during KARO install.

---

## 4. Build & Push the Operator Image

From the KARO repo root:

```bash
export IMAGE_REPO="${REGION}-docker.pkg.dev/${GCP_PROJECT}/${AR_REPO}"

# Build the operator image
docker build -t ${IMAGE_REPO}/karo-operator:0.4.0-alpha .

# Push to Artifact Registry
docker push ${IMAGE_REPO}/karo-operator:0.4.0-alpha
```

For multi-architecture (if your cluster uses ARM nodes):

```bash
docker buildx create --use
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ${IMAGE_REPO}/karo-operator:0.4.0-alpha \
  --push .
```

---

## 5. Build & Push Agent Harness Images

The dev-team example uses two harness images:

```bash
# Claude Code harness (used by planner + reviewer agents)
docker build -t ${IMAGE_REPO}/karo-claude-code-harness:latest \
  -f harness/claude-code/Dockerfile harness/claude-code/

docker push ${IMAGE_REPO}/karo-claude-code-harness:latest

# Goose harness (used by coder + test agents)
docker build -t ${IMAGE_REPO}/karo-goose-harness:latest \
  -f harness/goose/Dockerfile harness/goose/

docker push ${IMAGE_REPO}/karo-goose-harness:latest
```

---

## 6. Install the KARO Operator

### Create a GKE-specific values override file

```bash
cat > /tmp/karo-gke-values.yaml <<'EOF'
replicaCount: 2

image:
  # Replace with your Artifact Registry path
  repository: us-central1-docker.pkg.dev/YOUR_PROJECT/karo/karo-operator
  tag: "0.4.0-alpha"
  pullPolicy: IfNotPresent

installCRDs: true

webhook:
  enabled: true
  certManagerEnabled: true    # requires cert-manager installed above

rbac:
  create: true
  serviceAccount:
    create: true
    name: karo-operator
    # Uncomment for Workload Identity (needed if using Vertex AI):
    # annotations:
    #   iam.gke.io/gcp-service-account: karo-operator@YOUR_PROJECT.iam.gserviceaccount.com

leaderElection:
  enabled: true
  namespace: karo-system

metrics:
  enabled: true
  serviceMonitor:
    enabled: false

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

observability:
  enabled: true
  metrics:
    port: 8080
    path: /metrics
  vmServiceScrape:
    enabled: false    # set to true if VictoriaMetrics operator is installed
  vmPodScrape:
    enabled: false    # set to true if VictoriaMetrics operator is installed
  vmRule:
    enabled: false    # set to true if VictoriaMetrics operator is installed
  grafanaDashboard:
    enabled: false    # set to true if Grafana is installed
EOF
```

**Replace `YOUR_PROJECT`** with your actual GCP project ID.

### Install via Helm

```bash
helm install karo charts/karo/ \
  --namespace karo-system \
  --create-namespace \
  --values /tmp/karo-gke-values.yaml
```

### Verify operator is running

```bash
kubectl get pods -n karo-system
kubectl get crds | grep karo.dev
```

Expected output — 2 operator pods running, 14 CRDs registered:

```
NAME                             READY   STATUS    RESTARTS   AGE
karo-operator-7b8f5c6d4-abc12   1/1     Running   0          30s
karo-operator-7b8f5c6d4-def34   1/1     Running   0          30s
```

```
agentchannels.karo.dev
agentinstances.karo.dev
agentloops.karo.dev
agentmailboxes.karo.dev
agentpolicies.karo.dev
agentspecs.karo.dev
agentteams.karo.dev
dispatchers.karo.dev
evalsuites.karo.dev
memoryconfigs.karo.dev
modelconfigs.karo.dev
sandboxclasses.karo.dev
taskgraphs.karo.dev
toolsets.karo.dev
```

---

## 7. Configure Secrets for the Dev Team

Before deploying the dev-team example, replace the placeholder values in `examples/dev-team/01-secrets.yaml`.

### Option A: Edit the file directly

```bash
# Edit and replace all <your-...-key> placeholders
vi examples/dev-team/01-secrets.yaml
```

### Option B: Create secrets imperatively (recommended — avoids committing secrets)

```bash
kubectl create namespace dev-team
kubectl label namespace dev-team team=dev karo.dev/managed=true

# Anthropic API key (required)
kubectl create secret generic anthropic-api-key \
  -n dev-team \
  --from-literal=ANTHROPIC_API_KEY="sk-ant-..."

# GitHub token (required for coder agent)
kubectl create secret generic github-mcp-credentials \
  -n dev-team \
  --from-literal=GITHUB_TOKEN="ghp_..."

kubectl create secret generic coder-git-token \
  -n dev-team \
  --from-literal=GITHUB_TOKEN="ghp_..."

# mem0 API key (required for shared memory)
kubectl create secret generic mem0-api-key \
  -n dev-team \
  --from-literal=API_KEY="m0-..."

# Slack (required for AgentChannel approvals)
kubectl create secret generic slack-app-credentials \
  -n dev-team \
  --from-literal=BOT_TOKEN="xoxb-..."

kubectl create secret generic slack-signing-secret \
  -n dev-team \
  --from-literal=SIGNING_SECRET="..."
```

### Minimal setup (Anthropic only)

If you just want to test the core agent flow without all integrations:

```bash
kubectl create namespace dev-team
kubectl label namespace dev-team team=dev karo.dev/managed=true

kubectl create secret generic anthropic-api-key \
  -n dev-team \
  --from-literal=ANTHROPIC_API_KEY="sk-ant-..."

# Create dummy secrets so CRD validation passes
kubectl create secret generic github-mcp-credentials -n dev-team --from-literal=GITHUB_TOKEN="placeholder"
kubectl create secret generic coder-git-token -n dev-team --from-literal=GITHUB_TOKEN="placeholder"
kubectl create secret generic mem0-api-key -n dev-team --from-literal=API_KEY="placeholder"
kubectl create secret generic slack-app-credentials -n dev-team --from-literal=BOT_TOKEN="placeholder"
kubectl create secret generic slack-signing-secret -n dev-team --from-literal=SIGNING_SECRET="placeholder"
```

---

## 8. Deploy the Dev Team Example

### Update image references

The dev-team manifests reference `ghcr.io/karo-dev/karo-*-harness:latest`. If you built custom images in step 5, update the image references in `04-agents.yaml`:

```bash
# Replace harness image references with your Artifact Registry images
sed -i "s|ghcr.io/karo-dev/karo-claude-code-harness:latest|${IMAGE_REPO}/karo-claude-code-harness:latest|g" examples/dev-team/04-agents.yaml
sed -i "s|ghcr.io/karo-dev/karo-goose-harness:latest|${IMAGE_REPO}/karo-goose-harness:latest|g" examples/dev-team/04-agents.yaml
```

### Apply the resources (in order)

```bash
# 1. Namespace (already created if you did step 7 Option B)
kubectl apply -f examples/dev-team/00-namespace.yaml

# 2. Secrets (skip if created imperatively above)
# kubectl apply -f examples/dev-team/01-secrets.yaml

# 3. Infrastructure: ModelConfig, SandboxClass, MemoryStore, ToolSet, AgentPolicy, EvalSuite
kubectl apply -f examples/dev-team/02-infrastructure.yaml

# 4. Agent system prompts
kubectl apply -f examples/dev-team/03-configmaps.yaml

# 5. Agent definitions (4 agents)
kubectl apply -f examples/dev-team/04-agents.yaml

# 6. Team + Dispatcher
kubectl apply -f examples/dev-team/05-team.yaml

# 7. TaskGraph (this kicks off the DAG execution)
kubectl apply -f examples/dev-team/06-taskgraph.yaml

# 8. Loop + Channel (cron trigger + Slack)
kubectl apply -f examples/dev-team/07-loop-channel.yaml
```

Or apply everything at once (if secrets file is populated):

```bash
kubectl apply -f examples/dev-team/
```

### If you skipped gVisor

Edit `02-infrastructure.yaml` before applying and remove the `runtimeClassName`:

```yaml
# Comment out or remove this line in the SandboxClass:
# runtimeClassName: gvisor
```

---

## 9. Verify the Deployment

### Check all KARO resources

```bash
# CRD resources in the dev-team namespace
kubectl get modelconfig,sandboxclass,memorystore,toolset,agentpolicy,evalsuite -n dev-team
kubectl get agentspec,agentteam,dispatcher -n dev-team
kubectl get taskgraph,agentloop,agentchannel -n dev-team
```

### Watch the TaskGraph progress

```bash
kubectl get taskgraph feature-auth -n dev-team -o yaml | grep -A 50 'status:'

# Or watch continuously
kubectl get taskgraph -n dev-team -w
```

### Check for agent instances being created

When the Dispatcher routes the first task (`design-api` → `planner-agent`), an AgentInstance is created:

```bash
kubectl get agentinstances -n dev-team
kubectl get pods -n dev-team
```

### Check operator logs for errors

```bash
kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator --tail=100 -f
```

### Check individual task status

```bash
# View the status of each task in the graph
kubectl get taskgraph feature-auth -n dev-team -o jsonpath='{.status.taskStatuses}' | jq .
```

---

## 10. Using Vertex AI (No API Keys)

If you prefer to use Vertex AI with Claude (via Workload Identity) instead of direct Anthropic API keys:

### Step 1: Create a GCP Service Account

```bash
gcloud iam service-accounts create karo-agent-sa \
  --display-name="KARO Agent Service Account"

# Grant Vertex AI access
gcloud projects add-iam-policy-binding $GCP_PROJECT \
  --member="serviceAccount:karo-agent-sa@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"
```

### Step 2: Bind to Kubernetes Service Account (Workload Identity)

```bash
# Allow the KSA to impersonate the GSA
gcloud iam service-accounts add-iam-policy-binding \
  karo-agent-sa@${GCP_PROJECT}.iam.gserviceaccount.com \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:${GCP_PROJECT}.svc.id.goog[dev-team/default]"

# Annotate the KSA
kubectl annotate serviceaccount default \
  -n dev-team \
  iam.gke.io/gcp-service-account=karo-agent-sa@${GCP_PROJECT}.iam.gserviceaccount.com
```

### Step 3: Replace the ModelConfig

Replace the `claude-sonnet` ModelConfig in `02-infrastructure.yaml`:

```yaml
apiVersion: karo.dev/v1alpha1
kind: ModelConfig
metadata:
  name: claude-sonnet
  namespace: dev-team
spec:
  provider: google-vertex
  name: claude-sonnet-4@20250514
  # No apiKeySecret needed — Workload Identity handles auth
  vertex:
    project: YOUR_GCP_PROJECT_ID
    location: us-central1
  parameters:
    maxTokens: 8192
    temperature: 0.3
```

The ModelConfig controller will inject `VERTEX_PROJECT`, `VERTEX_LOCATION`, and `VERTEX_MODEL_ID` environment variables into agent pods automatically.

---

## 11. Observability Setup

### Prometheus metrics

The KARO operator exposes 60+ Prometheus metrics on `:8080/metrics`. Key metrics:

| Metric | Description |
|--------|-------------|
| `karo_taskgraph_task_duration_seconds` | Task execution time histogram |
| `karo_taskgraph_tasks_total` | Total tasks by status |
| `karo_dispatcher_routing_total` | Dispatch routing decisions |
| `karo_agentinstance_lifecycle_total` | Agent pod lifecycle events |
| `karo_agentmailbox_pending_messages` | Pending mailbox messages per agent |
| `karo_evalgate_pass_rate` | Eval gate pass rate |

### Enable VictoriaMetrics scraping

If you installed VictoriaMetrics Operator, update your Helm values:

```yaml
observability:
  vmServiceScrape:
    enabled: true
  vmPodScrape:
    enabled: true
  vmRule:
    enabled: true
  grafanaDashboard:
    enabled: true
```

Then upgrade:

```bash
helm upgrade karo charts/karo/ \
  --namespace karo-system \
  --values /tmp/karo-gke-values.yaml
```

### GKE Cloud Monitoring integration

For native GCP monitoring, forward metrics to Cloud Monitoring using the [Prometheus-to-Cloud-Monitoring sidecar](https://cloud.google.com/stackdriver/docs/managed-prometheus):

```bash
# GKE managed collection is enabled by default on new clusters
kubectl get operatorconfigs -n gmp-public
```

Managed Prometheus on GKE scrapes PodMonitoring resources. Create one for the KARO operator:

```yaml
apiVersion: monitoring.googleapis.com/v1
kind: PodMonitoring
metadata:
  name: karo-operator
  namespace: karo-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: karo-operator
  endpoints:
    - port: 8080
      interval: 15s
      path: /metrics
```

---

## 12. Troubleshooting

### Operator pods not starting

```bash
kubectl describe pods -n karo-system -l app.kubernetes.io/name=karo-operator
kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator --previous
```

Common causes:
- **ImagePullBackOff**: Image not pushed or registry auth not configured. Run `gcloud auth configure-docker`.
- **CrashLoopBackOff**: Check logs — usually a missing CRD or webhook cert issue.
- **Webhook TLS errors**: Ensure cert-manager is running and the Certificate resource exists.

### CRDs not registered

```bash
kubectl get crds | grep karo.dev
# Should show 14 CRDs. If empty:
helm get manifest karo -n karo-system | grep "kind: CustomResourceDefinition" | wc -l
```

### TaskGraph stuck in Pending

```bash
# Check Dispatcher status
kubectl get dispatcher dev-router -n dev-team -o yaml

# Check if mailboxes are created
kubectl get agentmailboxes -n dev-team

# Check operator logs for dispatch errors
kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator | grep -i "dispatcher\|taskgraph"
```

### Agent pods not created

```bash
# Check AgentInstance status
kubectl get agentinstances -n dev-team -o wide

# Check for scheduling issues
kubectl describe agentinstance <name> -n dev-team

# Check if sandbox node pool exists (if using gVisor)
kubectl get nodes -l sandbox.gke.io/runtime=gvisor
```

### Agent pods crash / OOMKilled

Increase resources in `04-agents.yaml`:

```yaml
runtime:
  resources:
    limits:
      cpu: "4"        # up from "2"
      memory: "8Gi"   # up from "4Gi"
```

### Secret validation errors

```bash
kubectl get secret anthropic-api-key -n dev-team -o jsonpath='{.data.ANTHROPIC_API_KEY}' | base64 -d
# Should output your API key (not empty)
```

---

## 13. Cleanup

### Remove the dev-team example

```bash
kubectl delete -f examples/dev-team/
```

### Uninstall the KARO operator

```bash
helm uninstall karo -n karo-system
kubectl delete namespace karo-system
```

### Delete CRDs (removes all KARO data)

```bash
kubectl get crds -o name | grep karo.dev | xargs kubectl delete
```

### Delete the GKE cluster

```bash
gcloud container clusters delete $CLUSTER_NAME --region $REGION
```

### Delete the Artifact Registry repository

```bash
gcloud artifacts repositories delete $AR_REPO --location=$REGION
```

---

## Resource Sizing Reference

| Component | CPU Request | Memory Request | CPU Limit | Memory Limit | Notes |
|-----------|------------|---------------|-----------|-------------|-------|
| KARO Operator (x2) | 100m | 128Mi | 500m | 512Mi | Leader election ensures only 1 active |
| Planner Agent | 500m | 1Gi | 2 | 4Gi | Light — mostly LLM calls |
| Coder Agent | 1 | 2Gi | 4 | 8Gi | Heavy — runs code, builds |
| Reviewer Agent | 500m | 1Gi | 2 | 4Gi | Light — reads code |
| Test Agent | 500m | 1Gi | 2 | 4Gi | Medium — runs test suites |

**Cluster sizing for the dev-team example:**
- Minimum: 2x `e2-standard-4` nodes (8 vCPU, 32 GB total)
- Recommended: 3x `e2-standard-4` with autoscaling up to 6 nodes
- With gVisor sandbox pool: add 1x `e2-standard-4` sandbox node

**Estimated GCP cost:** ~$200-400/month for a dev cluster running 8h/day with autoscaling.
