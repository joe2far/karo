#!/usr/bin/env bash
# hack/local/apply-dev-team.sh — Apply the dev-team example to the local cluster.
#
# This:
#   1. Creates the dev-team namespace + label
#   2. Creates secrets from environment variables (or placeholders if unset)
#   3. Rewrites the example's harness image references to the locally-loaded
#      images (built by hack/local/up.sh) and applies all manifests
#
# Required env (recommended):
#   ANTHROPIC_API_KEY   real Anthropic key — without this, agent calls fail
#
# Optional env (placeholder values are used when unset):
#   GITHUB_TOKEN, MEM0_API_KEY, SLACK_BOT_TOKEN, SLACK_SIGNING_SECRET
#
# Re-running is safe: secrets and manifests are upserted via `kubectl apply`.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

NS="${NS:-dev-team}"
IMAGE_TAG="${IMAGE_TAG:-local}"
HARNESS_CC_IMG="${HARNESS_CC_IMG:-ghcr.io/joe2far/karo-harness-claude-code:${IMAGE_TAG}}"
HARNESS_GS_IMG="${HARNESS_GS_IMG:-ghcr.io/joe2far/karo-harness-goose:${IMAGE_TAG}}"

log()  { printf '\033[1;34m[karo-local]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[karo-local]\033[0m %s\n' "$*" >&2; }

# 1. Namespace ------------------------------------------------------------
kubectl get ns "$NS" >/dev/null 2>&1 || kubectl create namespace "$NS"
kubectl label namespace "$NS" team=dev karo.dev/managed=true --overwrite >/dev/null

# 2. Secrets --------------------------------------------------------------
secret() {
  # secret <name> <key> <env-var>
  local name="$1" key="$2" var="$3"
  local raw="${!var-}"
  local val
  if [[ -z "$raw" ]]; then
    val="placeholder-$(echo "$var" | tr '[:upper:]' '[:lower:]')"
    warn "$var unset — using placeholder for secret/$name (calls will fail)"
  else
    val="$raw"
  fi
  kubectl create secret generic "$name" -n "$NS" \
    --from-literal="${key}=${val}" \
    --dry-run=client -o yaml | kubectl apply -f -
}

log "creating/updating secrets in namespace '$NS'"
secret anthropic-api-key      ANTHROPIC_API_KEY ANTHROPIC_API_KEY
secret github-mcp-credentials GITHUB_TOKEN      GITHUB_TOKEN
secret coder-git-token        GITHUB_TOKEN      GITHUB_TOKEN
secret mem0-api-key           API_KEY           MEM0_API_KEY
secret slack-app-credentials  BOT_TOKEN         SLACK_BOT_TOKEN
secret slack-signing-secret   SIGNING_SECRET    SLACK_SIGNING_SECRET

# 3. Rewrite + apply manifests -------------------------------------------
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

cp -r examples/dev-team/* "$TMPDIR/"

# Replace upstream harness images with the locally-loaded ones.
# Use python so we don't depend on GNU vs BSD sed differences (Mac-friendly).
python3 - "$TMPDIR" "$HARNESS_CC_IMG" "$HARNESS_GS_IMG" <<'PY'
import os, sys
root, cc, gs = sys.argv[1], sys.argv[2], sys.argv[3]
replacements = {
    "ghcr.io/karo-dev/karo-claude-code-harness:latest": cc,
    "ghcr.io/karo-dev/karo-goose-harness:latest": gs,
}
for dirpath, _, files in os.walk(root):
    for name in files:
        if not name.endswith((".yaml", ".yml")):
            continue
        path = os.path.join(dirpath, name)
        with open(path, "r") as fh:
            data = fh.read()
        new = data
        for old, repl in replacements.items():
            new = new.replace(old, repl)
        if new != data:
            with open(path, "w") as fh:
                fh.write(new)
PY

log "applying dev-team manifests with local image refs"
# Skip 01-secrets.yaml because we created secrets imperatively above.
for f in 00-namespace.yaml 02-infrastructure.yaml 03-configmaps.yaml \
         04-agents.yaml 05-team.yaml 06-taskgraph.yaml 07-loop-channel.yaml; do
  if [[ -f "$TMPDIR/$f" ]]; then
    log "kubectl apply -f $f"
    kubectl apply -f "$TMPDIR/$f"
  fi
done

cat <<EOF

dev-team example applied to namespace '$NS'.

Watch progress:
  kubectl get taskgraph -n $NS -w
  kubectl get agentinstances,pods -n $NS
  kubectl logs -n karo-system -l app.kubernetes.io/name=karo-operator -f
EOF
