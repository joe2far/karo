# karo-runtime

The shared Python library at the heart of KARO. It is imported by **both** the
KARO CLI (`cli/`) and the operator's agent-runtime image
(`agent-runtime-image/`), so the same code defines the `AgentTeam` contract and
runs the agent loop locally and on Kubernetes.

Contents:

- `spec/` — pydantic models, folder compiler, canonicalizer, loader, validator, JSON-Schema export
- `stores/` — store Protocols + the file backend (CLI); Redis/Postgres land in the operator lane
- `harness/` — the `HarnessAdapter` Protocol + the `sdk` adapter and local-only adapters
- `models/` — provider-agnostic model router
- `runtime/` — Coordinator, coordination patterns, authoritative budget meter, event vocabulary
- `exporter/` — `karo export` → KARO v2 manifest transform

See `docs/PRD-KARO-CLI.md` and `docs/PRD-KARO-v2.md` for the full spec.
