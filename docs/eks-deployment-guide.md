# KARO EKS Deployment Guide

Deploy the KARO operator and the **dev-team** example on Amazon Elastic Kubernetes Service.

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [EKS Cluster Setup](#2-eks-cluster-setup)
3. [Install Cluster Dependencies](#3-install-cluster-dependencies)
4. [Build & Push the Operator Image](#4-build--push-the-operator-image)
5. [Build & Push Agent Harness Images](#5-build--push-agent-harness-images)
6. [Install the KARO Operator](#6-install-the-karo-operator)
7. [Configure Secrets for the Dev Team](#7-configure-secrets-for-the-dev-team)
8. [Deploy the Dev Team Example](#8-deploy-the-dev-team-example)
9. [Verify the Deployment](#9-verify-the-deployment)
10. [Using Amazon Bedrock (No API Keys)](#10-using-amazon-bedrock-no-api-keys)
11. [Observability Setup](#11-observability-setup)
12. [Troubleshooting](#12-troubleshooting)
13. [Cleanup](#13-cleanup)

---

## 1. Prerequisites

### Tools

| Tool | Minimum Version | Install |
|------|----------------|---------|
| `aws` | latest | [Install Guide](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) |
| `eksctl` | 0.170+ | [Install Guide](https://eksctl.io/installation/) |
| `kubectl` | 1.28+ | [Install Guide](https://kubernetes.io/docs/tasks/tools/) |
| `helm` | 3.12+ | [Install Guide](https://helm.sh/docs/intro/install/) |
| `docker` | 24+ | [Install Guide](https://docs.docker.com/get-docker/) |

### AWS Configuration

Configure your AWS credentials and default region:

```bash
export AWS_REGION="us-west-2"
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export CLUSTER_NAME="karo-dev"

aws configure set default.region $AWS_REGION
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

## 2. EKS Cluster Setup

### Create an ECR repository (for images)

```bash
# Create repository for operator image
aws ecr create-repository \
  --repository-name karo/karo-operator \
  --region $AWS_REGION

# Create repositories for harness images
aws ecr create-repository \
  --repository-name karo/karo-claude-code-harness \
  --region $AWS_REGION

aws ecr create-repository \
  --repository-name karo/karo-goose-harness \
  --region $AWS_REGION
```

### Configure Docker to authenticate with ECR

```bash
aws ecr get-login-password --region $AWS_REGION | \
  docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com
```

### Create the EKS cluster

Using eksctl (recommended):

```bash
cat > /tmp/karo-eks-cluster.yaml <<EOF
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: ${CLUSTER_NAME}
  region: ${AWS_REGION}
  version: "1.31"

iam:
  withOIDC: true

managedNodeGroups:
  - name: karo-nodes
    instanceType: m5.xlarge
    desiredCapacity: 2
    minSize: 1
    maxSize: 6
    volumeSize: 100
    labels:
      workload: general
    tags:
      k8s.io/cluster-autoscaler/enabled: "true"
      k8s.io/cluster-autoscaler/${CLUSTER_NAME}: "owned"
    iam:
      withAddonPolicies:
        autoScaler: true
        cloudWatch: true
        ebs: true

addons:
  - name: vpc-cni
  - name: coredns
  - name: kube-proxy
  - name: aws-ebs-csi-driver
    serviceAccountRoleARN: arn:aws:iam::${AWS_ACCOUNT_ID}:role/AmazonEKS_EBS_CSI_DriverRole
EOF

eksctl create cluster -f /tmp/karo-eks-cluster.yaml
```

Key configuration explained:

| Setting | Why |
|---------|-----|
| `instanceType: m5.xlarge` | 4 vCPU / 16 GB — enough for operator + 2-3 concurrent agent pods |
| `minSize: 1, maxSize: 6` | Agent pods are ephemeral and bursty; autoscaling handles demand spikes |
| `withOIDC: true` | Enables IAM Roles for Service Accounts (IRSA) for Bedrock access |
| `aws-ebs-csi-driver` | Required for persistent volumes if needed by agents |

### Create IAM role for EBS CSI driver

```bash
eksctl create iamserviceaccount \
  --name ebs-csi-controller-sa \
  --namespace kube-system \
  --cluster ${CLUSTER_NAME} \
  --role-name AmazonEKS_EBS_CSI_DriverRole \
  --role-only \
  --attach-policy-arn arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy \
  --approve
```

### Get credentials

```bash
aws eks update-kubeconfig --region $AWS_REGION --name $CLUSTER_NAME
kubectl cluster-info
```

### (Optional) Install gVisor runtime class

The `dev-sandbox` SandboxClass in the dev-team example requires `runtimeClassName: gvisor`. On EKS, you can install gVisor using a DaemonSet:

```bash
kubectl apply -f https://raw.githubusercontent.com/google/gvisor/master/deploy/kubernetes/gvisor-runsc.yaml
```

Or create a Bottlerocket node group with containerd runtime (supports gVisor):

```bash
eksctl create nodegroup \
  --cluster=$CLUSTER_NAME \
  --name=sandbox-pool \
  --node-type=m5.xlarge \
  --nodes=1 \
  --nodes-min=0 \
  --nodes-max=4 \
  --node-ami-family=Bottlerocket \
  --region=$AWS_REGION
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

### (Optional) Install Cluster Autoscaler

For production use, enable the Kubernetes Cluster Autoscaler:

```bash
# Create IAM policy (if not already created via eksctl)
cat > /tmp/cluster-autoscaler-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "autoscaling:DescribeAutoScalingGroups",
        "autoscaling:DescribeAutoScalingInstances",
        "autoscaling:DescribeLaunchConfigurations",
        "autoscaling:DescribeScalingActivities",
        "autoscaling:DescribeTags",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeLaunchTemplateVersions"
      ],
      "Resource": ["*"]
    },
    {
      "Effect": "Allow",
      "Action": [
        "autoscaling:SetDesiredCapacity",
        "autoscaling:TerminateInstanceInAutoScalingGroup",
        "ec2:DescribeImages",
        "ec2:GetInstanceTypesFromInstanceRequirements",
        "eks:DescribeNodegroup"
      ],
      "Resource": ["*"]
    }
  ]
}
EOF

aws iam create-policy \
  --policy-name AmazonEKSClusterAutoscalerPolicy \
  --policy-document file:///tmp/cluster-autoscaler-policy.json

# Create service account with IAM role
eksctl create iamserviceaccount \
  --cluster=${CLUSTER_NAME} \
  --namespace=kube-system \
  --name=cluster-autoscaler \
  --attach-policy-arn=arn:aws:iam::${AWS_ACCOUNT_ID}:policy/AmazonEKSClusterAutoscalerPolicy \
  --override-existing-serviceaccounts \
  --approve

# Deploy cluster autoscaler
kubectl apply -f https://raw.githubusercontent.com/kubernetes/autoscaler/master/cluster-autoscaler/cloudprovider/aws/examples/cluster-autoscaler-autodiscover.yaml

# Patch deployment with cluster name
kubectl patch deployment cluster-autoscaler \
  -n kube-system \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"cluster-autoscaler","command":["./cluster-autoscaler","--v=4","--stderrthreshold=info","--cloud-provider=aws","--skip-nodes-with-local-storage=false","--expander=least-waste","--node-group-auto-discovery=asg:tag=k8s.io/cluster-autoscaler/enabled,k8s.io/cluster-autoscaler/'${CLUSTER_NAME}'"]}]}}}}'
```

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
export ECR_REGISTRY="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

# Build the operator image
docker build -t ${ECR_REGISTRY}/karo/karo-operator:0.4.0-alpha .

# Push to ECR
docker push ${ECR_REGISTRY}/karo/karo-operator:0.4.0-alpha
```

For multi-architecture (if your cluster uses ARM Graviton nodes):

```bash
docker buildx create --use
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ${ECR_REGISTRY}/karo/karo-operator:0.4.0-alpha \
  --push .
```

---

## 5. Build & Push Agent Harness Images

The dev-team example uses two harness images:

```bash
# Claude Code harness (used by planner + reviewer agents)
docker build -t ${ECR_REGISTRY}/karo/karo-claude-code-harness:latest \
  -f harness/claude-code/Dockerfile harness/claude-code/

docker push ${ECR_REGISTRY}/karo/karo-claude-code-harness:latest

# Goose harness (used by coder + test agents)
docker build -t ${ECR_REGISTRY}/karo/karo-goose-harness:latest \
  -f harness/goose/Dockerfile harness/goose/

docker push ${ECR_REGISTRY}/karo/karo-goose-harness:latest
```

---

## 6. Install the KARO Operator

### Create an EKS-specific values override file

```bash
cat > /tmp/karo-eks-values.yaml <<EOF
replicaCount: 2

image:
  # Replace with your ECR repository path
  repository: ${ECR_REGISTRY}/karo/karo-operator
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
    # Uncomment for IRSA (needed if using Amazon Bedrock):
    # annotations:
    #   eks.amazonaws.com/role-arn: arn:aws:iam::${AWS_ACCOUNT_ID}:role/karo-bedrock-access

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

### Install via Helm

```bash
helm install karo charts/karo/ \
  --namespace karo-system \
  --create-namespace \
  --values /tmp/karo-eks-values.yaml
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
# Replace harness image references with your ECR images
sed -i.bak "s|ghcr.io/karo-dev/karo-claude-code-harness:latest|${ECR_REGISTRY}/karo/karo-claude-code-harness:latest|g" examples/dev-team/04-agents.yaml
sed -i.bak "s|ghcr.io/karo-dev/karo-goose-harness:latest|${ECR_REGISTRY}/karo/karo-goose-harness:latest|g" examples/dev-team/04-agents.yaml
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

## 10. Using Amazon Bedrock (No API Keys)

If you prefer to use Amazon Bedrock with Claude (via IRSA) instead of direct Anthropic API keys:

### Step 1: Create an IAM role for Bedrock access

```bash
cat > /tmp/bedrock-trust-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::${AWS_ACCOUNT_ID}:oidc-provider/oidc.eks.${AWS_REGION}.amazonaws.com/id/$(aws eks describe-cluster --name ${CLUSTER_NAME} --query 'cluster.identity.oidc.issuer' --output text | cut -d '/' -f 5)"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.${AWS_REGION}.amazonaws.com/id/$(aws eks describe-cluster --name ${CLUSTER_NAME} --query 'cluster.identity.oidc.issuer' --output text | cut -d '/' -f 5):sub": "system:serviceaccount:dev-team:default",
          "oidc.eks.${AWS_REGION}.amazonaws.com/id/$(aws eks describe-cluster --name ${CLUSTER_NAME} --query 'cluster.identity.oidc.issuer' --output text | cut -d '/' -f 5):aud": "sts.amazonaws.com"
        }
      }
    }
  ]
}
EOF

cat > /tmp/bedrock-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:InvokeModel",
        "bedrock:InvokeModelWithResponseStream"
      ],
      "Resource": "arn:aws:bedrock:${AWS_REGION}::foundation-model/anthropic.claude-*"
    }
  ]
}
EOF

# Create IAM policy
aws iam create-policy \
  --policy-name KAROBedrockAccess \
  --policy-document file:///tmp/bedrock-policy.json

# Create IAM role
aws iam create-role \
  --role-name karo-bedrock-access \
  --assume-role-policy-document file:///tmp/bedrock-trust-policy.json

# Attach policy to role
aws iam attach-role-policy \
  --role-name karo-bedrock-access \
  --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/KAROBedrockAccess
```

### Step 2: Annotate the Kubernetes Service Account

```bash
kubectl annotate serviceaccount default \
  -n dev-team \
  eks.amazonaws.com/role-arn=arn:aws:iam::${AWS_ACCOUNT_ID}:role/karo-bedrock-access
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
  provider: amazon-bedrock
  name: anthropic.claude-sonnet-4-20250514-v1:0
  # No apiKeySecret needed — IRSA handles auth
  bedrock:
    region: us-west-2
  parameters:
    maxTokens: 8192
    temperature: 0.3
```

The ModelConfig controller will inject `AWS_REGION` and `BEDROCK_MODEL_ID` environment variables into agent pods automatically. The AWS SDK will use IRSA credentials from the pod's service account.

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
  --values /tmp/karo-eks-values.yaml
```

### AWS CloudWatch Container Insights

For native AWS monitoring, enable CloudWatch Container Insights:

```bash
# Install CloudWatch agent
aws eks create-addon \
  --cluster-name $CLUSTER_NAME \
  --addon-name amazon-cloudwatch-observability \
  --region $AWS_REGION

# Or use the CloudWatch agent via Helm
helm repo add aws-observability https://aws-observability.github.io/aws-otel-helm-charts
helm repo update

helm install aws-cloudwatch-metrics aws-observability/adot-exporter-for-eks-on-ec2 \
  --namespace amazon-cloudwatch \
  --create-namespace \
  --set clusterName=$CLUSTER_NAME
```

### Enable Prometheus metrics export to CloudWatch

```bash
# Install AWS Distro for OpenTelemetry (ADOT) collector
eksctl create iamserviceaccount \
  --name adot-collector \
  --namespace amazon-cloudwatch \
  --cluster $CLUSTER_NAME \
  --attach-policy-arn arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy \
  --approve \
  --override-existing-serviceaccounts

kubectl apply -f https://amazon-eks.s3.amazonaws.com/docs/addons/adot/latest/adot-collector-deployment.yaml
```

---

## 12. Troubleshooting

### Operator pods not starting

```bash
kubectl describe pods -n karo-system -l app.kubernetes.io/name=karo-operator
kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator --previous
```

Common causes:
- **ImagePullBackOff**: Image not pushed or ECR auth not configured. Re-run `aws ecr get-login-password` command.
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

# Check if nodes have sufficient capacity
kubectl top nodes
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

### ECR authentication issues

If pods can't pull images:

```bash
# Verify ECR login
aws ecr get-login-password --region $AWS_REGION | \
  docker login --username AWS --password-stdin ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com

# Check if images exist
aws ecr describe-images --repository-name karo/karo-operator --region $AWS_REGION
```

### Node scaling issues

Check cluster autoscaler logs:

```bash
kubectl logs -n kube-system deployment/cluster-autoscaler
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

### Delete the EKS cluster

```bash
eksctl delete cluster --name $CLUSTER_NAME --region $AWS_REGION
```

This will also delete:
- All node groups
- Associated IAM roles and policies
- VPC and networking resources created by eksctl

### Delete ECR repositories

```bash
aws ecr delete-repository \
  --repository-name karo/karo-operator \
  --region $AWS_REGION \
  --force

aws ecr delete-repository \
  --repository-name karo/karo-claude-code-harness \
  --region $AWS_REGION \
  --force

aws ecr delete-repository \
  --repository-name karo/karo-goose-harness \
  --region $AWS_REGION \
  --force
```

### Clean up IAM resources

```bash
# Detach and delete Bedrock policy
aws iam detach-role-policy \
  --role-name karo-bedrock-access \
  --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/KAROBedrockAccess

aws iam delete-role --role-name karo-bedrock-access
aws iam delete-policy --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/KAROBedrockAccess

# Delete cluster autoscaler policy
aws iam delete-policy --policy-arn arn:aws:iam::${AWS_ACCOUNT_ID}:policy/AmazonEKSClusterAutoscalerPolicy
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
- Minimum: 2x `m5.xlarge` nodes (8 vCPU, 32 GB total)
- Recommended: 3x `m5.xlarge` with autoscaling up to 6 nodes
- With gVisor sandbox pool: add 1x `m5.xlarge` Bottlerocket node

**Alternative instance types:**
- **Cost-optimized**: `m6a.xlarge` (AMD) or `t3.xlarge` (burstable)
- **Graviton (ARM)**: `m7g.xlarge` — 20-40% lower cost, requires multi-arch images
- **High-memory agents**: `r5.xlarge` (8 vCPU / 32 GB)

**Estimated AWS cost:** ~$150-350/month for a dev cluster running 8h/day with autoscaling (us-west-2 pricing).
