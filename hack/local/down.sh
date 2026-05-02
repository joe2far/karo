#!/usr/bin/env bash
# hack/local/down.sh — Tear down the local KARO kind cluster.
#
# By default this deletes the entire kind cluster created by up.sh.
# Use UNINSTALL_ONLY=true to keep the cluster but remove the helm release
# (useful for fast reinstall iteration).

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-karo-local}"
NAMESPACE="${NAMESPACE:-karo-system}"
RELEASE_NAME="${RELEASE_NAME:-karo}"
UNINSTALL_ONLY="${UNINSTALL_ONLY:-false}"

log() { printf '\033[1;34m[karo-local]\033[0m %s\n' "$*"; }

if [[ "$UNINSTALL_ONLY" == "true" ]]; then
  log "uninstalling helm release '$RELEASE_NAME' from '$NAMESPACE' (cluster preserved)"
  helm uninstall "$RELEASE_NAME" -n "$NAMESPACE" || true
  exit 0
fi

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "deleting kind cluster '$CLUSTER_NAME'"
  kind delete cluster --name "$CLUSTER_NAME"
else
  log "kind cluster '$CLUSTER_NAME' does not exist — nothing to do"
fi
