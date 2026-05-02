# KARO Local Deployment Guide (macOS)

Run KARO end-to-end on your MacBook so you can iterate on `ModelConfig`,
`AgentSpec`, `TaskGraph`, and other CRDs before pushing them to GKE.

This guide uses **kind** as the primary path. Notes for k3d and minikube are
included at the bottom — the wrapper scripts target kind, but the chart is
runtime-agnostic so all three work.

---

## Why kind for KARO?

| Tool      | Pros                                                                                | Cons                                                  |
|-----------|-------------------------------------------------------------------------------------|-------------------------------------------------------|
| **kind**  | Upstream k8s; `kind load docker-image` (no registry); webhooks + cert-manager just work; multi-node trivial | Needs Docker Desktop / colima |
| k3d       | Very fast; built-in registry; small footprint                                       | k3s ≠ upstream (Traefik, ServiceLB) — minor drift     |
| minikube  | Multiple drivers (docker, hyperkit, qemu); rich addon system                        | Heavier startup; image loading slower than kind       |

KARO's controllers are vanilla controller-runtime + a validating webhook with
cert-manager. **kind gives the closest GKE behavior locally**, which is what
you want when refining specs that will ship to GKE.

---

## 1. Prerequisites

Install once:

```bash
brew install kind kubectl helm
# Plus a container runtime — choose one:
brew install --cask docker        # Docker Desktop, easiest
# or
brew install colima docker        # Colima as a Docker Desktop alternative
colima start --cpu 4 --memory 8
```

Sanity check:

```bash
docker info >/dev/null && echo "docker ok"
kind version
kubectl version --client
helm version --short
```

Recommended Mac resources: **4 vCPU / 8 GB RAM** to Docker / Colima. The
operator itself is small, but cert-manager + a few agent pods add up.

You'll also need API keys for any model providers your specs reference.
For a first run only `ANTHROPIC_API_KEY` is required.

---

## 2. One-shot: bring everything up

From the repo root:

```bash
make local-up
```

This runs `hack/local/up.sh`, which:

1. Creates a 2-node kind cluster (`karo-local`) using `hack/local/kind-config.yaml`
2. Installs **cert-manager** (required by the operator's validating webhook)
3. Builds the operator, the `agent-runtime-mcp` sidecar, and all four harness images
4. Loads the images into the kind nodes via `kind load docker-image`
5. Installs the KARO Helm chart with `hack/local/values-local.yaml`
6. Waits for the operator to become Ready

Expect ~3–5 minutes the first time (image builds dominate). Re-runs are
faster because Docker layers and the cluster are reused.

Verify:

```bash
kubectl get pods -n karo-system
kubectl get crds | grep karo.dev    # 14 CRDs
```

---

## 3. Iterate on model specs

The whole point of running locally is fast feedback on `ModelConfig`,
`AgentSpec`, etc. The Helm chart enables the validating webhook by default,
so misconfigured specs are rejected immediately.

```bash
# Apply your draft spec
kubectl apply -f my-modelconfig.yaml

# Watch the operator process it
kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator -f
# or:
make local-logs

# Inspect status
kubectl get modelconfig -A
kubectl describe modelconfig <name> -n <ns>
```

Typical iteration loop:

```bash
# 1. Edit your spec
vim specs/dev-modelconfig.yaml

# 2. Apply
kubectl apply -f specs/dev-modelconfig.yaml

# 3. If validation fails, the apply is rejected — fix and retry.
# 4. If it applies, check status fields populated by the controller:
kubectl get modelconfig dev-claude -o yaml | yq '.status'
```

When you're happy with the spec, check it into git and apply to GKE.

---

## 4. Try the dev-team example

The `examples/dev-team/` example exercises most of the CRDs together
(ModelConfig + ToolSet + AgentSpec + AgentTeam + TaskGraph + Dispatcher +
EvalSuite + AgentChannel). It's a great smoke test that everything wires up.

```bash
# 1. Provide secrets via env (only ANTHROPIC_API_KEY is required for agent calls)
cp hack/local/secrets.example.env hack/local/secrets.env
$EDITOR hack/local/secrets.env
set -a; source hack/local/secrets.env; set +a

# 2. Apply
make local-dev-team

# 3. Watch
kubectl get taskgraph -n dev-team -w
kubectl get agentinstances,pods -n dev-team
```

The script rewrites the example's harness image references to the
locally-loaded tags (`:local`), so no registry pulls are needed.

> The dev-team example references `runtimeClassName: gvisor` in its
> `SandboxClass`. kind nodes don't have gVisor, so the AgentInstance
> controller will fall back gracefully *unless* PodSecurity is set strict.
> If pods fail to schedule, edit `examples/dev-team/02-infrastructure.yaml`
> and remove the `runtimeClassName` line before applying.

---

## 5. Common operations

```bash
# Rebuild and reload images after editing operator/harness code
make local-images

# Reinstall the helm release without rebuilding images (e.g. chart edits)
make local-install

# Tail operator logs
make local-logs

# Uninstall the chart but keep the cluster (fast helm-only iteration)
make local-uninstall

# Tear down the entire cluster
make local-down
```

Useful direct commands:

```bash
# kubeconfig context
kubectl config use-context kind-karo-local

# Forward operator metrics
kubectl -n karo-system port-forward svc/karo-metrics 8080:8080
curl -s localhost:8080/metrics | grep karo_

# Get into the operator pod
kubectl -n karo-system exec -it deploy/karo-operator -- sh
```

---

## 6. Promoting from local → GKE

Local validation does not cover everything. Things that only show up on GKE:

- **Image pulls** — locally we use `kind load`; on GKE you push to Artifact
  Registry. Make sure your tags match what `values-gke.yaml` expects.
- **Workload Identity** — required for Vertex AI.
- **gVisor / sandbox node pools** — only available on GKE sandbox pools.
- **VictoriaMetrics / Prometheus operator** — disabled in `values-local.yaml`.
- **Network policies and node selectors** — always test on GKE before relying
  on them.

Once a spec works locally, follow `docs/gke-deployment-guide.md` to push it
through the Artifact Registry build and apply to your GKE cluster.

---

## 7. Troubleshooting

### `kind create cluster` hangs or fails

```bash
docker info       # ensure docker / colima is running
docker ps -a      # check for an old kind container blocking
kind delete cluster --name karo-local
make local-up
```

If you're on Apple Silicon and seeing `exec format error`, make sure
`docker buildx` is using the host platform (`linux/arm64`). The default
build doesn't cross-compile, so the kind nodes (also `arm64`) match.

### Operator pod is `ImagePullBackOff`

`kind load` skipped because the build failed silently:

```bash
docker images | grep karo
# Re-run with verbose build:
SKIP_HARNESSES=true hack/local/up.sh
```

Make sure `image.pullPolicy: IfNotPresent` (set in `values-local.yaml`) —
otherwise kubelet tries to pull from ghcr.io and fails.

### Webhook errors on `kubectl apply`

```
Error from server (InternalError): error when creating "...":
  Internal error occurred: failed calling webhook
  "vmodelconfig.kb.io": failed to call webhook: ... x509: certificate signed
  by unknown authority
```

cert-manager hasn't injected the CA yet. Wait 30s and retry, or:

```bash
kubectl -n cert-manager get pods
kubectl get certificate -A
kubectl describe certificate -n karo-system karo-webhook-cert
```

If you want to skip webhooks entirely for a quick spec check, install with:

```bash
helm upgrade --install karo charts/karo -n karo-system --create-namespace \
  -f hack/local/values-local.yaml \
  --set webhook.enabled=false \
  --set image.tag=local
```

(Validation is then only the CRD's OpenAPI schema, not the operator's logic.)

### Agent pods stuck `Pending`

Most likely the kind worker is out of resources. Either:

- Bump Docker / Colima resources (`colima stop && colima start --cpu 6 --memory 12`)
- Lower `runtime.resources` in your `AgentSpec`
- Reduce `maxInstances` in the `AgentSpec.scaling` block

### `kubectl get taskgraph` shows tasks stuck `Pending`

```bash
# Dispatcher logs first
kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator | grep -i dispatcher

# Mailboxes for the agents
kubectl get agentmailboxes -A
kubectl describe agentmailbox <name> -n dev-team
```

Common causes: capability mismatch between TaskGraph task `type` and
AgentSpec `capabilities`; missing `ModelConfig`; `AgentPolicy` rejecting
the model.

---

## Appendix A: Using k3d instead of kind

```bash
brew install k3d
k3d cluster create karo-local --servers 1 --agents 1 \
  --port "8080:80@loadbalancer" --wait
# k3d auto-imports images from the host:
k3d image import ghcr.io/joe2far/karo-operator:local --cluster karo-local

# Then install cert-manager and the chart exactly as in `up.sh`.
helm install cert-manager jetstack/cert-manager -n cert-manager --create-namespace \
  --version v1.14.5 --set crds.enabled=true --wait
helm upgrade --install karo charts/karo -n karo-system --create-namespace \
  -f hack/local/values-local.yaml --set image.tag=local
```

k3s ships its own service load balancer and Traefik. KARO doesn't depend on
either, so they're harmless — but if you also install another ingress
controller, disable Traefik with `--k3s-arg '--disable=traefik@server:0'`.

## Appendix B: Using minikube instead of kind

```bash
brew install minikube
minikube start --cpus 4 --memory 8192 --kubernetes-version v1.30.4
# Use minikube's docker daemon so locally-built images are available:
eval $(minikube docker-env)
docker build -t ghcr.io/joe2far/karo-operator:local .
# ... build the rest of the images ...

helm install cert-manager jetstack/cert-manager -n cert-manager --create-namespace \
  --version v1.14.5 --set crds.enabled=true --wait
helm upgrade --install karo charts/karo -n karo-system --create-namespace \
  -f hack/local/values-local.yaml --set image.tag=local
```

`eval $(minikube docker-env)` is the equivalent of `kind load`. Remember
that this redirects `docker` for the current shell only.
