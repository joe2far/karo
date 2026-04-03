# Running Gemma 4 with KARO: Deployment Options & Cost Analysis

This guide covers deploying [Gemma 4](https://blog.google/innovation-and-ai/technology/developers-tools/gemma-4/) as a model backend for KARO agents, comparing self-hosted (GKE + vLLM) vs managed (Vertex AI) vs Anthropic Claude API options.

---

## Table of Contents

1. [Gemma 4 Overview](#1-gemma-4-overview)
2. [Deployment Option A: Self-Hosted on GKE with vLLM](#2-deployment-option-a-self-hosted-on-gke-with-vllm)
3. [Deployment Option B: Vertex AI Managed Endpoint](#3-deployment-option-b-vertex-ai-managed-endpoint)
4. [Deployment Option C: Anthropic Claude API](#4-deployment-option-c-anthropic-claude-api)
5. [Cost Comparison](#5-cost-comparison)
6. [KARO ModelConfig Examples](#6-karo-modelconfig-examples)
7. [Recommendations](#7-recommendations)

---

## 1. Gemma 4 Overview

Gemma 4 is Google's open-source model family (Apache 2.0 license), released April 2026. It is the most capable open model family available, with native multimodal support (text, image, audio).

### Model Variants

| Variant | Parameters | Context Window | GPU Requirement | Best For |
|---------|-----------|----------------|-----------------|----------|
| **Gemma 4 E2B** | 2.3B effective | 128K | Mobile / edge device | On-device, ultra-low latency |
| **Gemma 4 E4B** | 4.5B effective | 128K | 8GB GPU (laptop) | Edge / lightweight tasks |
| **Gemma 4 26B MoE** | 26B total / 3.8B active | 128K | 1x 24GB GPU (Q4) | Cost-efficient reasoning |
| **Gemma 4 31B Dense** | 31B | 256K | 1x 80GB H100 (FP16) | Best quality, agentic workflows |

All variants support text and image inputs natively. The E2B and E4B variants also support audio.

---

## 2. Deployment Option A: Self-Hosted on GKE with vLLM

Run Gemma 4 on your own GKE cluster using [vLLM](https://vllm.ai/blog/gemma4), which has Day-0 support for all Gemma 4 variants.

### Prerequisites

- GKE cluster with GPU node pool
- vLLM container image (v0.8+ with Gemma 4 support)

### GPU Node Pool Sizing

| Model | GPU Type | GPUs | Node Type | On-Demand $/hr | Spot $/hr (est.) |
|-------|----------|------|-----------|-----------------|------------------|
| Gemma 4 31B (FP16) | H100 80GB | 1 | a3-highgpu-1g | ~$3.00 | ~$1.00 |
| Gemma 4 31B (FP16, high throughput) | H100 80GB | 2 (TP=2) | a3-highgpu-2g | ~$6.00 | ~$2.00 |
| Gemma 4 31B (FP8/INT8) | L4 24GB | 2 (TP=2) | g2-standard-24 | ~$1.40 | ~$0.50 |
| Gemma 4 26B MoE (Q4) | L4 24GB | 1 | g2-standard-12 | ~$0.70 | ~$0.25 |

> **Note:** Prices are approximate GCP on-demand rates as of April 2026. Spot/preemptible instances offer 60-70% savings but can be reclaimed.

### GKE Node Pool Creation

```bash
# For Gemma 4 31B on H100
gcloud container node-pools create vllm-h100 \
  --cluster=$CLUSTER_NAME \
  --zone=$ZONE \
  --machine-type=a3-highgpu-1g \
  --accelerator=type=nvidia-h100-80gb,count=1 \
  --num-nodes=1 \
  --spot  # Use spot for cost savings; remove for production reliability

# For Gemma 4 26B MoE or quantized 31B on L4
gcloud container node-pools create vllm-l4 \
  --cluster=$CLUSTER_NAME \
  --zone=$ZONE \
  --machine-type=g2-standard-12 \
  --accelerator=type=nvidia-l4,count=1 \
  --num-nodes=1 \
  --spot
```

### vLLM Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vllm-gemma4
  namespace: karo-models
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vllm-gemma4
  template:
    metadata:
      labels:
        app: vllm-gemma4
    spec:
      nodeSelector:
        cloud.google.com/gke-accelerator: nvidia-h100-80gb
      containers:
      - name: vllm
        image: vllm/vllm-openai:latest
        args:
        - "--model=google/gemma-4-31b-it"
        - "--tensor-parallel-size=1"
        - "--max-model-len=32768"
        - "--gpu-memory-utilization=0.90"
        - "--port=8000"
        ports:
        - containerPort: 8000
        resources:
          limits:
            nvidia.com/gpu: "1"
          requests:
            nvidia.com/gpu: "1"
            memory: "80Gi"
            cpu: "8"
---
apiVersion: v1
kind: Service
metadata:
  name: vllm-gemma4
  namespace: karo-models
spec:
  selector:
    app: vllm-gemma4
  ports:
  - port: 8000
    targetPort: 8000
```

### Estimated Throughput

Based on community benchmarks for similar 27-31B models on vLLM:

| Setup | Output Tokens/sec | Concurrent Requests |
|-------|-------------------|---------------------|
| 1x H100, TP=1, FP16 | ~800-1,200 | 8-16 |
| 2x H100, TP=2, FP16 | ~1,500-2,200 | 16-32 |
| 1x L4, Q4 quantized | ~200-400 | 4-8 |

### Monthly Cost Estimate (Self-Hosted)

Assuming 24/7 operation:

| Setup | On-Demand/mo | Spot/mo | CUDs (3yr)/mo |
|-------|-------------|---------|---------------|
| 1x H100 | ~$2,160 | ~$720 | ~$1,300 |
| 2x L4 (quantized) | ~$1,008 | ~$360 | ~$600 |
| 1x L4 (26B MoE) | ~$504 | ~$180 | ~$300 |

**Tokens included:** Unlimited — you pay for compute, not tokens.

---

## 3. Deployment Option B: Vertex AI Managed Endpoint

Since Gemma 4 is open-source, Vertex AI charges only for the **compute infrastructure** (GPU hours), not per-token. This is the key cost advantage over proprietary models.

### Deploy via Vertex AI Model Garden

```bash
# Deploy Gemma 4 31B to a Vertex AI endpoint
gcloud ai endpoints create \
  --region=$REGION \
  --display-name=gemma-4-31b-it

# Deploy model to endpoint (uses vLLM under the hood)
gcloud ai models upload \
  --region=$REGION \
  --display-name=gemma-4-31b-it \
  --container-image-uri=us-docker.pkg.dev/vertex-ai/prediction/vllm-serve:latest \
  --artifact-uri=gs://vertex-model-garden-public-us/gemma4/gemma-4-31b-it
```

### Vertex AI Pricing (Open Models)

For open models like Gemma 4 on Vertex AI, you pay for the accelerator time:

| Accelerator | $/hr (on-demand) | $/hr (1yr CUD) |
|-------------|------------------|-----------------|
| NVIDIA L4 | ~$0.70 | ~$0.45 |
| NVIDIA H100 | ~$3.00 | ~$1.80 |

**Vertex AI adds a management overhead** (~10-20% above raw GKE GPU cost) but handles autoscaling, health checks, and model serving infrastructure.

### Monthly Cost Estimate (Vertex AI)

| Setup | On-Demand/mo | CUDs/mo |
|-------|-------------|---------|
| 1x H100 endpoint (24/7) | ~$2,400 | ~$1,400 |
| 1x H100 endpoint (8hr/day) | ~$800 | N/A |
| Autoscaled (min 0, scale to 1) | Pay only when serving | — |

> **Key advantage:** Vertex AI supports scale-to-zero. If your agents are not active 24/7, you only pay when inference is happening.

---

## 4. Deployment Option C: Anthropic Claude API

For comparison, using Claude models via the Anthropic API (the current default in KARO examples).

### Pricing (April 2026)

| Model | Input $/1M tokens | Output $/1M tokens | Context |
|-------|-------------------|---------------------|---------|
| Claude Opus 4.6 | $5.00 | $25.00 | 1M |
| Claude Sonnet 4.6 | $3.00 | $15.00 | 1M |
| Claude Haiku 4.5 | $1.00 | $5.00 | 200K |

**Cost optimizations available:**
- Prompt caching: up to 90% savings on repeated context
- Batch API: 50% discount for non-real-time workloads
- Combined: up to 95% savings

---

## 5. Cost Comparison

### Scenario: Moderate Agent Workload

Assume a team of 5 agents, each processing ~500K input tokens and ~100K output tokens per day.

**Daily token volume:** 2.5M input + 500K output = 3M tokens/day

| Option | Monthly Cost | Notes |
|--------|-------------|-------|
| **Gemma 4 31B on GKE (1x L4, quantized, spot)** | **~$360** | Unlimited tokens, lower quality than Claude |
| **Gemma 4 26B MoE on GKE (1x L4, spot)** | **~$180** | Cheapest, good for simpler tasks |
| **Gemma 4 31B on GKE (1x H100, spot)** | **~$720** | Best self-hosted quality + throughput |
| **Gemma 4 on Vertex AI (autoscaled)** | **~$400-800** | Managed, scale-to-zero possible |
| **Claude Haiku 4.5** | **~$525** | $75M×$1 + $15M×$5 |
| **Claude Sonnet 4.6** | **~$1,575** | $75M×$3 + $15M×$15 |
| **Claude Sonnet 4.6 (batch + cache)** | **~$160-400** | With 50% batch + caching |
| **Claude Opus 4.6** | **~$4,125** | $75M×$5 + $15M×$25 |

### Scenario: High-Volume Production

Assume 20 agents, 2M input + 500K output tokens per agent per day.

**Daily token volume:** 40M input + 10M output = 50M tokens/day

| Option | Monthly Cost | Notes |
|--------|-------------|-------|
| **Gemma 4 31B on GKE (2x H100, spot)** | **~$1,440** | May need 2 GPUs for throughput |
| **Gemma 4 on Vertex AI (2x H100)** | **~$4,800** | Managed, autoscaled |
| **Claude Haiku 4.5** | **~$8,700** | Cheapest Claude option at scale |
| **Claude Sonnet 4.6** | **~$26,100** | High cost at volume |
| **Claude Sonnet 4.6 (batch + cache)** | **~$2,600-6,500** | Significant savings with optimizations |

### Key Takeaways

1. **Self-hosted Gemma 4 on GKE is the cheapest option at scale** — you pay fixed GPU cost regardless of token volume. The more tokens you process, the better the economics.
2. **Vertex AI** adds management convenience (~10-20% premium over raw GKE) and supports scale-to-zero, making it cheaper for bursty/low-utilization workloads.
3. **Claude with batch + caching** can be competitive at moderate volumes, while offering superior reasoning quality.
4. **Gemma 4 31B quality** is strong for code generation, structured output, and agentic tasks, but Claude Sonnet/Opus still leads on complex multi-step reasoning and nuanced instruction following.

### When to Choose What

| Choose | When |
|--------|------|
| **Gemma 4 on GKE (vLLM)** | High token volume, cost is primary concern, acceptable quality for your use case, you have GPU ops expertise |
| **Gemma 4 on Vertex AI** | Want managed infrastructure, bursty workloads, scale-to-zero matters, no GPU ops team |
| **Claude Sonnet/Opus** | Quality is critical, complex reasoning tasks, moderate token volumes, or with batch+caching optimizations |
| **Claude Haiku** | Need API simplicity + reasonable cost, quality requirements are moderate |
| **Hybrid (Gemma 4 + Claude)** | Route simple tasks to Gemma 4, escalate complex reasoning to Claude — use KARO's ModelConfig per-agent |

---

## 6. KARO ModelConfig Examples

### Gemma 4 via Self-Hosted vLLM (OpenAI-compatible endpoint)

Since vLLM exposes an OpenAI-compatible API, use the `openai` provider with a custom endpoint:

```yaml
apiVersion: karo.dev/v1alpha1
kind: ModelConfig
metadata:
  name: gemma4-31b-vllm
  namespace: karo-agents
spec:
  provider: openai
  name: google/gemma-4-31b-it

  # Point to the in-cluster vLLM service (no API key needed for in-cluster)
  apiKeySecret:
    name: vllm-credentials
    key: API_KEY  # Can be a dummy value like "not-needed" for in-cluster vLLM
  endpoint: "http://vllm-gemma4.karo-models.svc.cluster.local:8000/v1"

  parameters:
    maxTokens: 8192
    temperature: 0.3
    topP: 0.95

  rateLimit:
    requestsPerMinute: 120
    tokensPerMinute: 200000
    tokensPerDay: 5000000
```

### Gemma 4 via Vertex AI

```yaml
apiVersion: karo.dev/v1alpha1
kind: ModelConfig
metadata:
  name: gemma4-31b-vertex
  namespace: karo-agents
spec:
  provider: vertex
  name: gemma-4-31b-it

  vertex:
    project: "my-gcp-project"
    location: "us-central1"
    gcpServiceAccount: "karo-vertex-sa@my-gcp-project.iam.gserviceaccount.com"

  parameters:
    maxTokens: 8192
    temperature: 0.3
    topP: 0.95

  rateLimit:
    requestsPerMinute: 60
    tokensPerMinute: 100000
    tokensPerDay: 3000000
```

### Hybrid Setup: Route by Task Complexity

Create both ModelConfigs and assign them to different agents:

```yaml
# Cheap model for simple tasks (code formatting, summarization)
apiVersion: karo.dev/v1alpha1
kind: ModelConfig
metadata:
  name: gemma4-31b-vllm
  namespace: karo-agents
spec:
  provider: openai
  name: google/gemma-4-31b-it
  apiKeySecret:
    name: vllm-credentials
    key: API_KEY
  endpoint: "http://vllm-gemma4.karo-models.svc.cluster.local:8000/v1"
  parameters:
    maxTokens: 8192
    temperature: 0.2
---
# High-quality model for complex reasoning
apiVersion: karo.dev/v1alpha1
kind: ModelConfig
metadata:
  name: claude-sonnet
  namespace: karo-agents
spec:
  provider: anthropic
  name: claude-sonnet-4-20250514
  apiKeySecret:
    name: anthropic-credentials
    key: ANTHROPIC_API_KEY
  parameters:
    maxTokens: 8192
    temperature: 0.3
```

Then reference them in separate AgentSpec definitions:

```yaml
# Simple-task agent uses Gemma 4
apiVersion: karo.dev/v1alpha1
kind: AgentSpec
metadata:
  name: formatter-agent
spec:
  modelConfigRef:
    name: gemma4-31b-vllm
  # ...

---
# Complex-task agent uses Claude
apiVersion: karo.dev/v1alpha1
kind: AgentSpec
metadata:
  name: architect-agent
spec:
  modelConfigRef:
    name: claude-sonnet
  # ...
```

---

## 7. Recommendations

### For cost-conscious teams starting out

1. **Start with Gemma 4 26B MoE on a single L4 GPU (spot)** — ~$180/mo for unlimited tokens
2. Use the `openai` provider in ModelConfig pointing at your vLLM endpoint
3. Benchmark your agents' task quality; if insufficient, upgrade to Gemma 4 31B or switch specific agents to Claude

### For production workloads

1. **Use Vertex AI with autoscaling** if you want managed infrastructure and have bursty workloads
2. **Use GKE + vLLM with CUDs** if you have sustained high throughput and a platform team to manage GPUs
3. **Consider a hybrid approach** — route simple/high-volume tasks to Gemma 4, reserve Claude for complex reasoning

### For maximum quality

1. **Claude Sonnet 4.6 with batch API + prompt caching** offers the best quality-to-cost ratio
2. Use Gemma 4 as a fallback or for preprocessing/simple extraction tasks

---

## Sources

- [Gemma 4 on vLLM](https://vllm.ai/blog/gemma4)
- [Gemma 4 announcement](https://blog.google/innovation-and-ai/technology/developers-tools/gemma-4/)
- [GKE GPU serving with vLLM](https://docs.cloud.google.com/kubernetes-engine/docs/tutorials/serve-gemma-gpu-vllm)
- [Vertex AI pricing](https://cloud.google.com/vertex-ai/generative-ai/pricing)
- [GCP GPU pricing](https://cloud.google.com/compute/gpus-pricing)
- [Claude API pricing](https://platform.claude.com/docs/en/about-claude/pricing)
- [Gemma 4 developer guide](https://www.lushbinary.com/blog/gemma-4-developer-guide-benchmarks-architecture-local-deployment-2026/)
- [vLLM Gemma 4 recipes](https://docs.vllm.ai/projects/recipes/en/latest/Google/Gemma4.html)
