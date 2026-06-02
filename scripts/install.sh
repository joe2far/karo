#!/usr/bin/env bash
# Install the `karo` CLI without cloning or building anything yourself.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/joe2far/karo/main/scripts/install.sh | bash
#   # or, from a checkout:
#   ./scripts/install.sh [--ref <branch-or-tag>] [--pip]
#
# By default this uses pipx (isolated, on your PATH) if available, falling back
# to pip. Pass --pip to force pip.
set -euo pipefail

REPO="https://github.com/joe2far/karo.git"
REF="main"
FORCE_PIP=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref) REF="$2"; shift 2;;
    --pip) FORCE_PIP=1; shift;;
    -h|--help) sed -n '2,12p' "$0"; exit 0;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

RUNTIME="karo-runtime @ git+${REPO}@${REF}#subdirectory=karo-runtime"
CLI="karo @ git+${REPO}@${REF}#subdirectory=cli"

if [[ "$FORCE_PIP" -eq 0 ]] && command -v pipx >/dev/null 2>&1; then
  echo "Installing karo with pipx (ref: ${REF})..."
  # Install the CLI; inject the shared runtime from the same ref into its venv.
  pipx install "git+${REPO}@${REF}#subdirectory=cli" --force
  pipx inject karo "git+${REPO}@${REF}#subdirectory=karo-runtime" --force
else
  echo "Installing karo with pip (ref: ${REF})..."
  python3 -m pip install --upgrade "${RUNTIME}" "${CLI}"
fi

echo
karo version
echo
echo "Done. Try:  karo init --name my-team --template lead-team && karo validate"
