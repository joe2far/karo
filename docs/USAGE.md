# KARO usage guide

A hands-on guide to authoring, running, and steering an agent team locally with
the `karo` CLI. Everything here runs offline in a deterministic **stub mode** —
no API keys required — so you can learn the whole workflow before wiring up a
real model. The same `AgentTeam` you build here is what `karo export` hands to
Kubernetes (KARO v2), unchanged.

If you just want the command list, see the table in the [root README](../README.md#command-reference-cli)
or `karo --help`. This guide explains the *model* behind the commands. For one
end-to-end developer journey — scaffold a dev team, sling a feature from a file,
**open a PR for review**, share it, and graduate to Kubernetes with a git
**service account** — follow [`DEV-WORKFLOW.md`](DEV-WORKFLOW.md), which is honest
about which steps work today and which are roadmap seams.

---

## 1. Install

You do **not** need to clone the repo or run a build. Pick one:

```bash
# A. From PyPI-style git installs (no clone, no build step you run yourself):
pip install \
  "karo-runtime @ git+https://github.com/joe2far/karo.git@main#subdirectory=karo-runtime" \
  "karo         @ git+https://github.com/joe2far/karo.git@main#subdirectory=cli"

# B. Isolated, on your PATH, via pipx (recommended for a CLI):
pipx install "git+https://github.com/joe2far/karo.git@main#subdirectory=cli" \
  --pip-args="karo-runtime@git+https://github.com/joe2far/karo.git@main#subdirectory=karo-runtime"

# C. From a local clone (editable, for hacking on KARO itself):
pip install -e karo-runtime -e cli
```

Then:

```bash
karo version
karo doctor        # checks SDK + harness binaries + credential profiles
```

`karo doctor` will report `claude-agent-sdk: not installed` — that's fine. Without
it, the `sdk` harness falls back to the offline stub, which is all you need for
this guide. Install it (`pip install 'karo-runtime[sdk]'`) and set credentials
when you want real model calls.

---

## 2. The authoring model: a folder is the source

You author a **folder**; `karo compile` turns it into the canonical `AgentTeam`
(the build artifact). Scaffold one — note that `karo init` scaffolds into the
**current directory**, so create the folder first:

```bash
mkdir refactor-team && cd refactor-team
karo init --name refactor-team --template lead-team
```

```
refactor-team/
  karo.yaml            # thin: team name, defaults, coordination, budgets, agent refs
  agents/
    planner/AGENT.md   # frontmatter (harness, model, refs) + body = the system prompt
    implementer/AGENT.md
    reviewer/AGENT.md
  skills/              # Claude Code-style skill dirs (each has a SKILL.md)
  tools/               # custom in-process @tool functions (auto-discovered)
  mcp/servers.yaml     # MCP server declarations
  shared/              # reusable fragments pulled in via include:
  .karo/               # local runtime state (memory/tasks/mail) — gitignored
```

Templates:

| Template | Shape |
|---|---|
| `minimal` | one agent, runs the objective end to end |
| `lead-team` | a lead that decomposes + delegates to teammates, with a reviewer (default) |
| `pipeline` | a fixed stage sequence; each stage depends on the previous |
| `--flat` | a single inline `team.yaml` instead of a folder (one-off teams) |

Always validate before running — it's static, no network, no model calls:

```bash
karo validate                 # local rules
karo validate --target cluster  # also the cluster-portability rules
```

---

## 3. Running a team: `karo run`

There are **two ways** to give a team work.

### 3a. Run the whole team toward an objective

```bash
karo run -o "tidy up the logging module"
```

The **lead** (for `lead-and-teammates`) decomposes the objective into one task
per teammate, teammates execute, an optional `reviewer` reviews each result, and
the lead synthesizes a final report. Everything is persisted under `.karo/`, so
runs are inspectable and resumable.

Live output is the event stream — task transitions, model usage, mailbox
handoffs:

```
· turn.start       implementer    turn_id=t0
· model.usage      implementer    provider=anthropic prompt_tokens=40 completion_tokens=23 estimated=True
· task.transition  implementer    task_id=task-3259… from_state=in-progress to_state=review
· turn.start       reviewer       turn_id=t1
· task.transition  planner        task_id=task-5300… from_state=in-progress to_state=done

run run-a0f1aaa25a: completed
  - task-3259… [done] owner=implementer
  - task-5300… [done] owner=planner
```

Useful flags:

| Flag | Effect |
|---|---|
| `-o, --objective` | the objective text (or `--objective-file`) |
| `--dry-run` | plan the task graph only — no model calls |
| `--max-turns N` | cap total turns (good for a quick smoke run) |
| `--autonomy supervised\|autonomous` | override the team/agent autonomy |
| `--resume <run-id>` | continue a persisted run |
| `--json` | machine-readable result |

### 3b. Sling a prompt straight at one agent

When you don't want the lead to decompose anything — you want **this prompt, on
this agent, now** — target it directly. Three equivalent ways:

```bash
karo sling reviewer "review the auth changes on JIRA-789"   # v1-style verb
karo run reviewer "review the auth changes on JIRA-789"     # positional target + message
karo run --agent reviewer -o "review the auth changes …"    # explicit flags
```

All three create a **single task owned by `reviewer`** and drive only that agent
— no planner decomposition, no synthesis. The agent's own model, tools, MCP
servers, skills, guards, and autonomy all still apply.

`karo run`'s positional grammar is:

| You type | Means |
|---|---|
| `karo run "objective"` | whole team (objective is positional — no `-o` needed) |
| `karo run agent "msg"` | one agent in the current project |
| `karo run team/agent "msg"` | one agent in the team folder `team/` (see §3c) |
| `karo run -o "objective"` | whole team (explicit flag, back-compat) |
| `karo run agent @prompt.md` | prompt read from a file (`@path`) — the prompt varies per task |
| `karo run agent -f prompt.md` | same, via the `-f/--file` flag (`--objective-file` is the old alias) |

The prompt/objective can always come from a **file** — handy when it's a long
task brief, a generated spec, or a templated request that changes per run:
`karo sling deploy-approver @release-checklist.md`.

`karo sling` is the same thing with the target **required**, so there's never any
ambiguity — use it when you specifically mean "fire at one agent."

If you name an agent that isn't on the team, you get a helpful error:

```
error: unknown agent 'ghost'; team has: planner, implementer, reviewer
```

> **This is the pattern for "an agent with a Jira MCP + an approve-deploy skill,
> fired off with a specific comment".** See [§7](#7-worked-example-fire-a-deploy-approval-at-one-agent)
> and the runnable [`examples/pm-team/`](../examples/pm-team/).

### 3c. Multiple teams: addressing one by name

A **team is a folder**. When you keep several team folders side by side, you can
address one by name with `team/agent` — KARO resolves `<team>` to a folder by
looking (in order) for a `karo.yaml` at: the name as a path, `./<team>`,
`./teams/<team>`, and a sibling `../<team>`.

```
workspace/
  teams/
    pm-team/   karo.yaml + agents/ …
    dev-team/  karo.yaml + agents/ …
```

```bash
cd workspace
karo sling pm-team/deploy-approver "approve JIRA-789"
karo sling dev-team/implementer    "refactor the method in repos"
```

No team part = the current project (`-p`, default cwd). For a one-off, point at a
folder explicitly: `karo -p ./some/team run agent "msg"`.

### 3d. Slinging at a team on a cluster: `--context`

The exact same grammar works against a **KARO v2 cluster**, where a team is a
**namespace**. Add `--context <kubecontext>` and `<team>` is read as the
namespace; KARO creates an `AgentTask` in it (via `kubectl`) for the operator to
run:

```bash
karo sling pm-team/deploy-approver "approve JIRA-789" --context kind-karo
# → created AgentTask karo-sling-deploy-approver-… in namespace pm-team
# watch:  kubectl --context kind-karo -n pm-team get agenttasks -w
```

Use `--dry-run` to print the `AgentTask` manifest without applying it. This needs
the KARO v2 operator installed and the team deployed to that namespace (via
`karo export` + `kubectl apply`, §8). The operator **scales the team from zero**
on the new task: it wakes the task's owner agent and the lead, the agent pods run
a long-lived claim loop, and the team scales back to zero when the work is done.

---

### 3e. Agents that work on a git repo (or several)

Most agents do their work *in a codebase*. Declare the repos once under
`resources.repos`, then point each agent at the one(s) it works on with a
`repos:` list in its `AGENT.md` frontmatter:

```yaml
# karo.yaml
spec:
  resources:
    repos:
      - name: api          # git branch/tag/SHA via `ref:`, path via `path:`
        url: https://github.com/acme/api.git
        ref: main
      - name: web
        url: git@github.com:acme/web.git
```

```yaml
# agents/backend-dev/AGENT.md (frontmatter)
---
name: backend-dev
harness: sdk
repos: [api]          # this agent works on the `api` repo
---
```

On `karo run`, KARO clones/updates each referenced repo into the workspace
(`./workspace/<name>` by default) and sets the agent's **working directory** to
its repo — an agent with exactly one repo runs *inside* it; an agent with several
runs in the workspace root that holds them all. Use `--no-repos` to skip cloning
(e.g. when you've already checked them out).

- **Multiple repos, multiple agents:** list as many as you need; each agent picks
  the subset it owns. Two agents can share a repo or work on different ones.
- **Auth stays yours:** cloning uses *your* git config (ssh keys, credential
  helper, or a token in the URL via `${env:GITHUB_TOKEN}`). The team definition
  carries **no** credentials — a colleague clones the same team and authenticates
  as themselves (§9).
- **On cluster:** the operator provisions a `clone-repos` init-container that
  clones the same repos from the same spec into a shared workspace, so "which
  repo this agent works on" stays portable. On a cluster there's no human runner
  to borrow auth from, so the team works as a **git service account** — reference
  a credentials Secret via `runtime.secrets["git"]` and the operator wires it
  into both the clone and the agent's `git push`. Full walkthrough:
  [`DEV-WORKFLOW.md` §5b](DEV-WORKFLOW.md#5b-git-credentials-on-cluster-a-service-account).

> **Validation:** `karo validate` flags an agent that references a repo you didn't
> declare (`unknown-repo`), just like unknown tools/skills/MCP servers.

## 4. Steering a run: attach, guards, and the human gate

Every agent is a live, steerable session — not an approval queue. Two mechanisms:

**Guards** pause an agent and flag it for a human. They are *not* approvals; they
are pause-and-wait points. The most useful one is `pauseBefore`, which pauses an
agent **before** it invokes a named (often irreversible) tool:

```yaml
# in an AGENT.md frontmatter
interaction:
  autonomy: supervised
  guards:
    - pauseBefore: ["mcp:jira/transition_issue"]
```

Guard matchers use KARO's tool syntax: built-in tool names (e.g. `Bash`),
custom tool names, or `mcp:<server>/<tool>` for MCP tools. Guards only fire for
**supervised** agents — an `autonomous` agent never pauses for a human.

When a guarded agent hits its gate, the run reports it:

```
· guard.pause      deploy-approver guard=pauseBefore:mcp:jira/transition_issue
run run-87788c0754: incomplete
  paused (attach to steer): deploy-approver
```

Inspect, then release it:

```bash
karo ps                                  # deploy-approver  paused:guard  task-…
karo attach deploy-approver              # watch the live transcript
karo attach deploy-approver -m "looks good, proceed"   # inject a direction
karo attach deploy-approver --interrupt  # interrupt the current turn
karo attach deploy-approver --continue   # release the guard, hand back to the coordinator
```

After `--continue`, re-run (`karo run --resume <id>` or just `karo run -o "…"`
again — the persisted task is reused) and the agent proceeds past the gate to
completion.

---

## 5. Inspecting durable state

The runtime persists everything under `.karo/`; these commands read it:

```bash
karo ps                       # agents and their state (running / idle / paused:guard)
karo tasks list               # the durable task layer
karo tasks show <task-id>     # full task record (JSON)
karo tasks retry|cancel|assign <task-id> [agent]
karo mail list <agent>        # an agent's mailbox
karo mail send <agent> --body "…"   # post a message to an agent
karo memory list --scope team # team/agent memory
karo budget status            # authoritative token budget
```

`karo mail send` + `karo run` is the other way to hand work to an agent: drop a
message in its mailbox, then run. For a one-shot prompt, `karo run --agent`
(§3b) is simpler.

---

## 6. Secrets and MCP servers

Never hard-code tokens. Declare MCP servers in `mcp/servers.yaml` and pull
credentials from the environment (`${env:…}`) or the local secret store
(`${secret:…}`):

```yaml
servers:
  - name: jira
    transport: stdio
    command: ["jira-mcp-server"]
    env:
      JIRA_URL: ${env:JIRA_URL}
      JIRA_EMAIL: ${env:JIRA_EMAIL}
      JIRA_API_TOKEN: ${env:JIRA_API_TOKEN}
```

Agents opt into a server by name in their frontmatter (`mcp: [jira]`). Manage
local secrets with:

```bash
karo secret set JIRA_API_TOKEN "…"
karo secret get JIRA_API_TOKEN
karo secret rm  JIRA_API_TOKEN
```

`karo export --strip-secrets` (the default) emits **references only**, never
values — secrets never land in a committed manifest.

---

## 7. Worked example: fire a deploy approval at one agent

The [`examples/pm-team/`](../examples/pm-team/) folder is a complete, runnable
team built around exactly this scenario: a `deploy-approver` agent that has the
**Jira MCP server** and an **`approve-deploy` skill**, with a human gate before
it moves a ticket. Walk through it:

```bash
cd examples/pm-team
karo validate

# Sling a deploy-approval at the single agent:
karo run --agent deploy-approver \
  -o "Approve deploy for JIRA-789: ship auth-service v2.3"
```

It runs the pre-flight checklist and then **pauses at the human gate** (because
the Jira transition is behind a `pauseBefore` guard):

```
· guard.pause      deploy-approver guard=pauseBefore:mcp:jira/transition_issue
run …: incomplete
  paused (attach to steer): deploy-approver
```

Release it and let the transition proceed:

```bash
karo attach deploy-approver --continue
karo run --agent deploy-approver -o "Approve deploy for JIRA-789: ship auth-service v2.3"
# → run …: completed
```

Or hand the whole release to the team and let the lead coordinate — the
autonomous `cr-reviewer` finishes, while the supervised `deploy-approver` still
stops at its gate:

```bash
karo run -o "Review and approve the JIRA-789 auth-service v2.3 release"
```

See the example's [README](../examples/pm-team/README.md) for the full
walkthrough, including how to swap the stub for a real Anthropic model and
real Jira credentials.

---

## 8. Handing off to Kubernetes

The same folder graduates to the operator with no spec rewrite:

```bash
karo export -o team-manifest.yaml --namespace agents
kubectl apply -f team-manifest.yaml      # requires the KARO v2 operator
```

`karo export`'s `spec` body equals your local spec after canonicalization — a
tested CI invariant. Local-only harnesses (`cursor`, `codex`, `claude-code`) are
rejected for the cluster unless you pass `--allow-local-harness`; only `sdk` is
cluster-capable.

---

## 9. Sharing a team with colleagues (without your creds or state)

The folder **is** the shareable artifact — and it's designed so nothing personal
travels with it:

- **Commit the folder to git** (`karo.yaml`, `agents/`, `skills/`, `tools/`,
  `mcp/`). A colleague clones it and runs `karo run` — that's the whole handoff.
- **Your run state never travels.** `.karo/` (tasks, mailboxes, memory, token
  usage) is gitignored by the scaffold. Each person gets their own local state.
- **Your credentials never travel.** Model and MCP secrets are `${env:…}` /
  `${secret:…}` *references*, never values. Your colleague sets their **own**
  `ANTHROPIC_API_KEY` (their own Claude) and their own `JIRA_API_TOKEN`, and the
  identical spec runs for them. `karo export --strip-secrets` (the default) emits
  references only — nothing sensitive lands in a manifest either.
- **Git repos clone as the runner.** `resources.repos` declares *which* repos
  agents work on (§3e), but cloning uses each person's own git auth — so a private
  repo works for whoever has access, with no shared tokens in the team.

So the sharing flow is just:

```bash
# you
git add my-team && git commit -m "share the team" && git push

# your colleague
git clone … && cd my-team
export ANTHROPIC_API_KEY=…        # their own creds
karo validate
karo run -o "…"                   # runs in their Claude, with their state
```

This is why KARO v1's `TeamBinding` (a name→credentials mapping CRD) is **gone in
v2**: the team is self-contained, and credentials bind at run time from the
environment of whoever runs it. Multiple teams just means multiple folders (§3c).
