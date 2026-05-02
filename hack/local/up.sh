#!/usr/bin/env bash
# hack/local/up.sh — Bring up a local KARO test cluster on kind.
#
# Steps:
#   1. Verify required tooling (docker, kind, kubectl, helm)
#   2. Create the kind cluster (idempotent)
#   3. Install cert-manager (required by the operator's webhook)
#   4. Build operator + harness images, load them into the kind cluster
#   5. Install / upgrade the KARO Helm chart with hack/local/values-local.yaml
#   6. Wait for the operator to become Ready
#
# Re-running this script is safe: cluster reuse, image rebuild, helm upgrade.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

CLUSTER_NAME="${CLUSTER_NAME:-karo-local}"
KIND_CONFIG="${KIND_CONFIG:-hack/local/kind-config.yaml}"
NAMESPACE="${NAMESPACE:-karo-system}"
RELEASE_NAME="${RELEASE_NAME:-karo}"
VALUES_FILE="${VALUES_FILE:-hack/local/values-local.yaml}"
IMAGE_TAG="${IMAGE_TAG:-local}"

OPERATOR_IMG="${OPERATOR_IMG:-ghcr.io/joe2far/karo-operator:${IMAGE_TAG}}"
MCP_IMG="${MCP_IMG:-ghcr.io/joe2far/karo-agent-runtime-mcp:${IMAGE_TAG}}"
HARNESS_CC_IMG="${HARNESS_CC_IMG:-ghcr.io/joe2far/karo-harness-claude-code:${IMAGE_TAG}}"
HARNESS_GS_IMG="${HARNESS_GS_IMG:-ghcr.io/joe2far/karo-harness-goose:${IMAGE_TAG}}"
HARNESS_CW_IMG="${HARNESS_CW_IMG:-ghcr.io/joe2far/karo-harness-claw-code:${IMAGE_TAG}}"

CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.14.5}"
SKIP_HARNESSES="${SKIP_HARNESSES:-false}"
SKIP_BUILD="${SKIP_BUILD:-false}"

log()  { printf '\033[1;34m[karo-local]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[karo-local]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[karo-local]\033[0m %s\n' "$*" >&2; exit 1; }

require() {
  local cmd="$1" hint="${2:-}"
  command -v "$cmd" >/dev/null 2>&1 || die "missing tool: $cmd${hint:+ ($hint)}"
}

require docker  "https://docs.docker.com/get-docker/"
require kind    "brew install kind"
require kubectl "brew install kubectl"
require helm    "brew install helm"

# 1. Cluster --------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "kind cluster '$CLUSTER_NAME' already exists — reusing"
else
  log "creating kind cluster '$CLUSTER_NAME'"
  kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG" --wait 120s
fi

kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null
log "kubectl context: $(kubectl config current-context)"

# 2. cert-manager ---------------------------------------------------------
if kubectl get ns cert-manager >/dev/null 2>&1; then
  log "cert-manager namespace exists — skipping install"
else
  log "installing cert-manager ${CERT_MANAGER_VERSION}"
  helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
  helm repo update >/dev/null
  helm install cert-manager jetstack/cert-manager \
    --namespace cert-manager --create-namespace \
    --version "$CERT_MANAGER_VERSION" \
    --set crds.enabled=true \
    --wait --timeout 5m
fi

# 3. Build images ---------------------------------------------------------
if [[ "$SKIP_BUILD" != "true" ]]; then
  log "building operator image: $OPERATOR_IMG"
  docker build -t "$OPERATOR_IMG" .

  log "building agent-runtime-mcp image: $MCP_IMG"
  docker build -f Dockerfile.runtime-mcp -t "$MCP_IMG" .

  if [[ "$SKIP_HARNESSES" != "true" ]]; then
    log "building claude-code harness: $HARNESS_CC_IMG"
    docker build -t "$HARNESS_CC_IMG" harness/claude-code
    log "building goose harness: $HARNESS_GS_IMG"
    docker build -t "$HARNESS_GS_IMG" harness/goose
    log "building claw-code harness: $HARNESS_CW_IMG"
    docker build -t "$HARNESS_CW_IMG" harness/claw-code
  else
    warn "SKIP_HARNESSES=true — harness images will need to be loaded later"
  fi
else
  warn "SKIP_BUILD=true — assuming images are already built"
fi

# 4. Load images into kind ------------------------------------------------
log "loading images into kind cluster"
kind load docker-image --name "$CLUSTER_NAME" "$OPERATOR_IMG"
kind load docker-image --name "$CLUSTER_NAME" "$MCP_IMG"
if [[ "$SKIP_HARNESSES" != "true" ]]; then
  kind load docker-image --name "$CLUSTER_NAME" "$HARNESS_CC_IMG"
  kind load docker-image --name "$CLUSTER_NAME" "$HARNESS_GS_IMG"
  kind load docker-image --name "$CLUSTER_NAME" "$HARNESS_CW_IMG"
fi

# 5. Helm install/upgrade -------------------------------------------------
log "installing/upgrading KARO Helm release '$RELEASE_NAME' in '$NAMESPACE'"
helm upgrade --install "$RELEASE_NAME" charts/karo \
  --namespace "$NAMESPACE" --create-namespace \
  -f "$VALUES_FILE" \
  --set image.repository="${OPERATOR_IMG%:*}" \
  --set image.tag="${OPERATOR_IMG##*:}" \
  --wait --timeout 5m

# 6. Verify ---------------------------------------------------------------
log "operator status:"
kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=karo-operator
log "registered CRDs:"
kubectl get crds -o name | grep karo.dev || warn "no karo.dev CRDs found"

cat <<EOF

KARO is up locally.

Next steps:
  - Apply your model specs:           kubectl apply -f path/to/your-modelconfig.yaml
  - Try the dev-team example:         hack/local/apply-dev-team.sh
  - Tail operator logs:               kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=karo-operator -f
  - Tear down:                        hack/local/down.sh

Cluster context: kind-${CLUSTER_NAME}
EOF
