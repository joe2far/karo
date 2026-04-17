# Security Team Example

This example deploys a security remediation team that responds to Jira issues created from
Wiz vulnerability findings. The team analyzes the finding, determines the upgrade path for
affected dependencies, and implements the fixes as pull requests across all affected repositories.

## Agents

- **Security Lead** (orchestrator) - Triages Wiz findings, coordinates remediation, reviews PRs
- **Vulnerability Analyst** (evaluator) - Analyzes CVEs, maps dependencies, determines upgrade paths
- **Remediation Engineer** (executor) - Implements dependency upgrades, runs tests, creates PRs

## How It Works

When a Wiz finding creates a Jira issue (via webhook or polling):
1. Security Lead reads the Jira issue and extracts CVE details
2. Security Lead identifies all affected repositories and creates per-repo tasks
3. Vulnerability Analyst analyzes each repo's dependency (direct vs transitive, breaking changes)
4. Remediation Engineer updates the dependency, regenerates lockfiles, runs tests
5. Remediation Engineer creates a PR with the security remediation template
6. Security Lead verifies all PRs address the CVE
7. Security Lead updates the Jira issue with PR links and requests human approval

## Architecture

```
  ┌────────────────┐
  │  Jira Webhook   │ (new Wiz finding issue)
  │  or AgentLoop   │ (poll every 4h)
  └────────┬───────┘
           │
  ┌────────▼───────┐
  │   TaskGraph     │ (wiz-finding-CVE-*)
  └────────┬───────┘
           │
  ┌────────▼───────┐
  │   Dispatcher    │
  │  security-router│
  └────────┬───────┘
     ┌─────┼──────────────┐
     ▼     ▼              ▼
  Security  Vuln        Remediation
  Lead      Analyst     Engineer
                        (x1-5 per repo)
```

## Typical Workflow

```
Wiz Finding (CVE-2026-XXXXX)
  → Jira Issue (SEC-4567)
    → Triage: identify affected repos
      → Per-repo analysis (parallel):
        → Determine upgrade path
        → Check for breaking changes
      → Per-repo fix (parallel):
        → Update dependency
        → Regenerate lockfile
        → Run tests
        → Create PR
      → Verify all PRs
      → Update Jira
      → Human approval → Merge
```

## Supported Package Managers

The team can handle dependency updates across:
- **Node.js**: package.json / package-lock.json / yarn.lock
- **Go**: go.mod / go.sum
- **Python**: requirements.txt / pyproject.toml / Pipfile
- **Java**: pom.xml / build.gradle
- **Ruby**: Gemfile / Gemfile.lock
- **Rust**: Cargo.toml / Cargo.lock
- **Docker**: Dockerfile base images

## Quick Start

```bash
kubectl create namespace security-team
kubectl apply -f examples/security-team/

# The team auto-triggers on new Jira issues (webhook) or polls every 4 hours
# To manually trigger a remediation:
kubectl apply -f examples/security-team/05-taskgraph.yaml
kubectl get taskgraph -n security-team -w
```
