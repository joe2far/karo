# Dev workflow: feature → PR → share → Kubernetes

This guide walks **one complete developer journey** end to end, the way you'd
actually do it:

1. Stand up a development team and hook it to a git repo.
2. Sling a feature task (from a file) at the dev agent.
3. Have the agent open a **pull request** for human review before merge.
4. Share the team with colleagues — skills and repo, but none of your local
   state or credentials.
5. Graduate the team to a shared Kubernetes cluster, swapping *your* git auth
   for a **service-account** credential.
6. Let teammates sling tasks at the cluster agent.

It is the companion to [`USAGE.md`](USAGE.md), which explains the command model
in depth. This guide is the *narrative* — the order you'd hit the commands in,
and what each step actually requires.

> **Honesty legend.** KARO ships a working local runtime and an operator that is
> further along on some seams than others. Each step below is tagged so you know
> what you can do today versus what you wire up yourself:
>
> | Tag | Meaning |
> |---|---|
> | ✅ **works today** | Implemented and runnable offline in stub mode. |
> | 🔧 **you provide** | The mechanism exists, but you supply the piece (a skill, a credential). |
> | 🔭 **roadmap seam** | Documented intended shape; not yet wired in the operator. The doc tells you exactly what's missing so you're not surprised. |

---

## 0. Install ✅

```bash
pip install -e karo-runtime -e cli       # from a clone, editable
# or the no-build git installs in USAGE.md §1
karo version
karo doctor                              # SDK not installed → stub mode, which is fine
```

Everything below runs **offline in stub mode** (deterministic, no API keys),
*except* the one step that talks to GitHub to open a real PR — that step is
clearly marked, and degrades to a dry-run without credentials.

---

## 1. Scaffold a dev team and hook it to a repo ✅

`karo init` scaffolds **into the current directory** — it does *not* create a
named subfolder. So make the folder yourself first:

```bash
mkdir dev-team && cd dev-team
karo init --name dev-team --template lead-team
```

You get the folder convention:

```
dev-team/
  karo.yaml                 # team name, defaults, coordination, budgets, agent refs
  agents/
    planner/AGENT.md        # the lead — decomposes objectives
    implementer/AGENT.md     # does the feature work
    reviewer/AGENT.md        # reviews results
  skills/                   # empty — you add skill dirs here (see §3)
  tools/example.py          # a sample in-process @tool
  mcp/servers.yaml          # MCP server declarations
  shared/                   # reusable fragments
```

> The `lead-team` template ships **no skills** (just an empty `skills/`). You add
> them — §3 shows how, and [`examples/pm-team/skills/approve-deploy/`](../examples/pm-team/skills/approve-deploy/SKILL.md)
> is a complete one to copy from.

**Hook the repo.** Declare the repo once under `resources.repos`, then point the
implementer at it in its frontmatter ([USAGE §3e](USAGE.md#3e-agents-that-work-on-a-git-repo-or-several)):

```yaml
# karo.yaml  → under spec:
  resources:
    repos:
      - name: app
        url: https://github.com/your-org/app.git
        ref: main
```

```yaml
# agents/implementer/AGENT.md frontmatter
---
name: implementer
harness: sdk
repos: [app]              # this agent works inside the `app` repo
---
```

On the next `karo run`, KARO clones `app` into `./workspace/app` and sets the
implementer's working directory to it. **Cloning uses *your* git config** (ssh
keys, credential helper, or a token in the URL via `${env:GITHUB_TOKEN}`) — the
team definition carries no credentials. Add `--no-repos` to skip cloning if you
already have it checked out.

```bash
karo validate                # static checks; flags unknown repos/tools/skills
```

---

## 2. Sling a feature task — from a file ✅

A real feature brief is more than a one-liner. Put it in a file and sling the
file at the implementer. This is the **sling + file input** flow:

```bash
cat > feature-brief.md <<'EOF'
# Feature: add a /healthz endpoint

Acceptance criteria:
- GET /healthz returns 200 with body {"status":"ok"}
- Covered by a unit test
- No new dependencies
EOF

karo sling implementer @feature-brief.md
#   ^^^^^ fire one task at one agent; @file reads the prompt from the file
```

`@feature-brief.md` (or `-f feature-brief.md`) reads the brief from disk so the
prompt can be a long, generated, or templated document that changes per run.
This works identically whether the target is a local folder agent or a cluster
agent (§5). The implementer runs *inside* `./workspace/app`, so its edits land in
the repo's working tree.

> In stub mode the agent doesn't write real code — it runs the deterministic
> coordinator so you can see the **task lifecycle** (assigned → in-progress →
> review → done). Install `karo-runtime[sdk]` and set `ANTHROPIC_API_KEY` for the
> agent to actually edit files.

---

## 3. Open a pull request for review before merge  🔧 you provide

This is the step the base templates **don't** give you, and it's worth being
explicit: KARO has no built-in "open a PR" tool or skill. The runtime gives the
agent a checked-out repo and the ability to run shell/`git`; *you* give it a
**skill** that turns finished work into a branch, a push, and a PR — and you put
a **human gate** in front of the irreversible step (the PR, or a later merge).

That's two pieces, both first-class KARO concepts:

### 3a. An `open-pr` skill

Skills are Claude-Code-style directories with a `SKILL.md`. Create
`skills/open-pr/SKILL.md`:

```markdown
---
name: open-pr
description: Branch, commit, push, and open a GitHub pull request for review.
---
When you have finished a feature and tests pass, open a PR — never push to the
default branch directly:

1. Create a feature branch:  `git switch -c feat/<short-slug>`
2. Stage and commit your changes with a clear message describing the feature.
3. Push the branch:  `git push -u origin feat/<short-slug>`
4. Open the pull request for human review (do not merge it yourself):
     `gh pr create --fill --base main --head feat/<short-slug>`
   If `gh` is unavailable or unauthenticated, print the branch name and the PR
   title/body you *would* open, and stop — a human opens it.

Leave the PR open. A reviewer merges after approval.
```

Attach it to the implementer (and declare it as a team resource so it's portable):

```yaml
# agents/implementer/AGENT.md frontmatter
skills: [open-pr]
```

The skill is just instructions + the tools the agent already has (`git`, and
`gh` if present). Authenticating `gh` is the **runner's** business — locally
that's your `gh auth login` or `GITHUB_TOKEN`; on the cluster it's a service
account (§5). The skill itself carries no credentials, so it's safe to commit and
share.

> **Why a skill and not a built-in?** Because "how we open PRs" is a team policy
> (branch naming, base branch, reviewers, whether to use `gh` vs. a GitHub MCP
> server). KARO keeps that in your shareable spec rather than hard-coding it. A
> GitHub MCP server is an equally valid swap — declare it in `mcp/servers.yaml`
> and have the skill call `mcp:github/create_pull_request` instead of `gh`.

### 3b. A human gate before merge

"Review before merge" means a human decides. Two ways, both already in KARO:

- **Leave merging to a human (simplest).** The skill opens the PR and stops;
  approval/merge happens in GitHub. Nothing else needed.
- **Gate an automated merge with a guard.** If you ever let an agent *merge*,
  put a `pauseBefore` guard on the merge tool so the agent pauses for a human
  ([USAGE §4](USAGE.md#4-steering-a-run-attach-guards-and-the-human-gate)):

  ```yaml
  # agents/implementer/AGENT.md frontmatter
  interaction:
    autonomy: supervised
    guards:
      - pauseBefore: ["Bash"]            # or mcp:github/merge_pull_request
  ```

  The agent pauses; you `karo attach implementer` to inspect, then `--continue`
  to release. This is the same mechanism the `pm-team` example uses to gate a
  Jira transition.

Putting it together, the feature loop is:

```bash
karo sling implementer @feature-brief.md      # implement
# → agent works in ./workspace/app, runs the open-pr skill, opens a PR (or dry-runs it)
karo ps                                        # see state; paused if you gated merge
```

---

## 4. Share the team — skills and repo, not your state or creds ✅

The folder **is** the shareable artifact, and it's designed so nothing personal
travels with it ([USAGE §9](USAGE.md#9-sharing-a-team-with-colleagues-without-your-creds-or-state)):

```bash
git add karo.yaml agents/ skills/ tools/ mcp/    # the team + the open-pr skill
git commit -m "dev-team: feature workflow with open-pr skill"
git push
```

A colleague clones it and is productive immediately:

```bash
git clone … && cd dev-team
export ANTHROPIC_API_KEY=…       # their own model creds
gh auth login                    # their own git/PR identity
karo validate
karo sling implementer @their-brief.md
```

What does **not** travel, by design:

- **Your run state.** `.karo/` (tasks, mailboxes, memory, token usage) is
  gitignored. Each person gets their own.
- **Your credentials.** Model and MCP secrets are `${env:…}` / `${secret:…}`
  *references*, never values. The `open-pr` skill names `gh`/`GITHUB_TOKEN` but
  contains no token.
- **Your git auth.** `resources.repos` says *which* repo the agent works on;
  cloning uses each person's own git auth, so a private repo works for whoever
  has access, with no shared tokens in the team.

So "share the agent including its skills and the repo it works on, but not my
local agent configuration" is exactly what committing the folder does.

---

## 5. Graduate to a shared Kubernetes cluster

The same folder graduates to the KARO v2 operator with no spec rewrite. Two
things change when you go from "my laptop" to "a shared cluster": there is **no
human runner** whose git/`gh` auth the agent can borrow, so you supply a
**service-account credential**; and the backends become Redis/Postgres instead of
files.

### 5a. Export and apply ✅

```bash
karo export -o dev-team.manifest.yaml --namespace dev-team
kubectl apply -f dev-team.manifest.yaml      # requires the KARO v2 operator
```

`karo export`'s `spec` body equals your local spec after canonicalization (a
tested CI invariant), and `--strip-secrets` (the default) emits credential
*references* only. Only the `sdk` harness is cluster-capable.

Install the operator first, per [`README` → Deploy](../README.md#deploy-kubernetes):

```bash
cd operator
make build manifests
helm upgrade --install karo charts/karo -n karo-system --create-namespace
```

### 5b. Git credentials on cluster: a service account

✅ **works today.** Locally, the agent clones the repo and pushes the PR branch **as you**. On a
cluster there is no "you" — the agent pod needs its own git identity. The KARO
model: keep that identity *out of the team spec* (so the spec stays shareable)
and provide it as a **Kubernetes Secret** the pod consumes when it clones and
when the `open-pr` skill pushes.

**Step 1 — create the service-account Secret** (a bot PAT scoped to the team's
repos, plus the commit identity):

```bash
kubectl -n dev-team create secret generic git-credentials \
  --from-literal=GITHUB_TOKEN=ghp_xxx \
  --from-literal=GIT_AUTHOR_NAME="dev-team-bot" \
  --from-literal=GIT_AUTHOR_EMAIL="dev-team-bot@your-org.com"
```

**Step 2 — reference it from the team's `runtime:` block** under the well-known
`git` key (the `runtime:` block is operator-only; it never travels with the
shared spec, so the secret reference stays out of what colleagues clone):

```yaml
# in the exported team manifest (or add it before kubectl apply)
spec:
  runtime:
    secrets:
      git: { name: git-credentials }
```

That's it. When the operator provisions an agent that declares `repos:`, it now:

1. adds a **`clone-repos` init-container** (same agent-runtime image, which ships
   `git`) that clones the agent's repos into a shared `/workspace` `emptyDir` —
   the cluster equivalent of the local `./workspace` clone, scoped per-agent just
   like `ensure_repos(only_agents=…)`;
2. installs a **token credential helper** + commit identity from the Secret into
   a shared, writable `HOME` volume, so both the clone and the agent container's
   later `git push` authenticate **as the service account**, not a person;
3. mounts `/workspace` + that `HOME` into the agent container and sets its working
   directory (inside the single repo it owns, or the workspace root for several).

The credential never lands in the manifest — only the Secret *reference* does,
and the token is supplied to git at call time via the helper. Teams with no repos
get the original single-container pod unchanged.

> **Scope notes (honest edges):**
> - **HTTPS token auth** is wired (the documented `GITHUB_TOKEN` path). **SSH-key**
>   auth (`git@…` remotes) is not — use HTTPS remotes in `resources.repos` on
>   cluster.
> - The image ships **`git`**, so *clone + commit + push* are real on cluster.
>   It does **not** ship **`gh`**; to have the agent *open* the PR, either layer
>   `gh` into a derived agent image or point the `open-pr` skill at a **GitHub MCP
>   server**. The skill already degrades gracefully when `gh` is absent.
> - This is unit-tested in `operator/internal/controller/provisioner_test.go`
>   (init-container, volumes, working dir, secret wiring), but **not** validated
>   against a live cluster from this repo's CI — bring your own cluster to
>   exercise the full path.

### 5c. Teammates sling at the cluster agent ✅

Once the team is deployed, **a team is a namespace** and any teammate with
kube access can sling at it with the exact same grammar plus `--context`
([USAGE §3d](USAGE.md#3d-slinging-at-a-team-on-a-cluster---context)):

```bash
karo sling dev-team/implementer @feature-brief.md --context my-cluster
# → creates an AgentTask in namespace dev-team; the operator scales the team
#   from zero, the agent claims the task, and scales back to zero when done.
kubectl --context my-cluster -n dev-team get agenttasks -w
```

Slinging itself (creating the `AgentTask`, scale-from-zero, the claim loop) is
implemented and envtest-tested. With §5b in place the woken pod also clones the
repo and pushes as the service account; opening the PR uses `gh`/a GitHub MCP
server per the §5b scope notes.

---

## Summary: what holds up, and what you supply

| Step | Status |
|---|---|
| 1. Scaffold dev team + hook repo | ✅ works (note: `init` scaffolds into cwd) |
| 2. Sling a feature brief from a file | ✅ works, local and `--context` |
| 3. Open a PR, gated before merge | 🔧 you add an `open-pr` skill + a guard; the building blocks are first-class |
| 4. Share skills + repo, not state/creds | ✅ works — commit the folder |
| 5a. Export + apply to k8s | ✅ works |
| 5b. Service-account git creds on cluster | ✅ works — `runtime.secrets["git"]` → clone init-container + push auth (HTTPS token; `gh`/MCP opens the PR) |
| 5c. Teammates sling via `--context` | ✅ works |

The honest one-line answer to "could a developer do this from the docs as
written?": **yes for the local feature→PR→share loop** (once you add the
`open-pr` skill, which this guide gives you), and **yes on cluster** — export,
sling, scale-from-zero, and now service-account repo clone + push are all wired;
the only piece you bring is the PR-open mechanism (`gh` in a derived image or a
GitHub MCP server) and a live cluster to run it on.
