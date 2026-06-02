"""``karo`` — the KARO CLI (CLI §7).

Define your agent team once, locally, in portable config. Run it on your laptop
today. Push the exact same definition to Kubernetes (KARO v2) tomorrow.
"""

from __future__ import annotations

import asyncio
import json as _json
import sys
from pathlib import Path
from typing import Optional

import typer
import yaml

import karo_runtime as kr
from karo_runtime.exporter import ExportError, export_manifest
from karo_runtime.runtime import Coordinator
from karo_runtime.spec import compile_path
from karo_runtime.spec.loader import LoadError
from karo_runtime.stores.file import FileMailboxStore, FileMemoryStore, FileTaskStore
from karo_runtime.stores.base import Message, TaskState

from . import templates
from .config import load_profiles

app = typer.Typer(
    add_completion=False,
    no_args_is_help=True,
    help="KARO CLI — portable agent teams that run locally and graduate to Kubernetes.",
)


class State:
    project: Path = Path(".")
    verbose: bool = False
    json: bool = False
    no_color: bool = False


state = State()


def _echo(msg: str, err: bool = False) -> None:
    typer.echo(msg, err=err)


def _emit_json(obj) -> None:
    typer.echo(_json.dumps(obj, indent=2, default=str))


def _fail(msg: str, code: int = 1) -> None:
    typer.echo(f"error: {msg}", err=True)
    raise typer.Exit(code)


@app.callback()
def main(
    project: Path = typer.Option(Path("."), "--project", "-p", help="Project folder (default cwd)."),
    verbose: bool = typer.Option(False, "--verbose", "-v", help="Verbose output."),
    json_out: bool = typer.Option(False, "--json", help="Machine-readable JSON output."),
    no_color: bool = typer.Option(False, "--no-color", help="Disable color."),
) -> None:
    state.project = project
    state.verbose = verbose
    state.json = json_out
    state.no_color = no_color


def _compile(team_file: Optional[Path] = None):
    target = team_file or state.project
    try:
        return compile_path(target)
    except LoadError as e:
        _fail(f"{e.file or target}: {e.message}")
    except Exception as e:  # pydantic validation etc.
        _fail(str(e))


# --------------------------------------------------------------------------- #
# init
# --------------------------------------------------------------------------- #
@app.command()
def init(
    name: str = typer.Option("my-team", "--name", help="Team name (DNS-1123)."),
    template: str = typer.Option("lead-team", "--template", help="minimal | lead-team | pipeline."),
    flat: bool = typer.Option(False, "--flat", help="Emit a single inline team.yaml instead of a folder."),
) -> None:
    """Scaffold a new project as the folder convention (§4.0)."""
    root = state.project
    if flat:
        root.mkdir(parents=True, exist_ok=True)
        out = root / "team.yaml"
        out.write_text(templates.flat_team_yaml(name), encoding="utf-8")
        _echo(f"wrote {out}")
        return
    if template not in {"minimal", "lead-team", "pipeline"}:
        _fail(f"unknown template {template!r}; choose minimal | lead-team | pipeline")
    files = templates.template_files(template, name)
    written = templates.write_files(root, files)
    _echo(f"scaffolded {template!r} team {name!r} in {root} ({len(written)} files)")
    _echo("next: edit agents/, then `karo validate` and `karo run -o \"...\"`")


# --------------------------------------------------------------------------- #
# compile
# --------------------------------------------------------------------------- #
@app.command()
def compile(
    output: Optional[Path] = typer.Option(None, "--output", "-o", help="Write to file (default stdout)."),
    format: str = typer.Option("yaml", "--format", help="yaml | json."),
) -> None:
    """Compile the folder into the canonical AgentTeam (team.yaml form)."""
    result = _compile()
    # mode="json" renders enums/paths as plain scalars so the YAML emitter (which
    # only handles basic tags) can represent the doc. Without it, yaml.safe_dump
    # raises RepresenterError on pydantic enum objects (e.g. <Backend.file>).
    doc = result.team.model_dump(by_alias=True, exclude_none=True, mode="json")
    if format == "json":
        text = kr.canonical_json(doc)
    else:
        text = yaml.safe_dump(doc, sort_keys=True, default_flow_style=False)
    if output:
        output.write_text(text, encoding="utf-8")
        _echo(f"wrote {output}")
    else:
        typer.echo(text)


# --------------------------------------------------------------------------- #
# validate
# --------------------------------------------------------------------------- #
@app.command()
def validate(
    target: str = typer.Option("local", "--target", help="local | cluster."),
) -> None:
    """Compile then statically validate (no network, no model calls)."""
    if target not in {"local", "cluster"}:
        _fail("--target must be local or cluster")
    result = _compile()
    # Gather raw texts for the numeric-literal lint.
    raw_texts: dict[str, str] = {}
    if state.project.is_dir():
        ky = state.project / "karo.yaml"
        if ky.exists():
            raw_texts["karo.yaml"] = ky.read_text("utf-8")
    diags = kr.validate(result, target=target, raw_texts=raw_texts)
    errors = [d for d in diags if d.severity == "error"]
    if state.json:
        _emit_json([d.to_dict() for d in diags])
    else:
        for d in diags:
            _echo(f"{d.file}:{d.line}: {d.severity}: [{d.code}] {d.message}")
        if not diags:
            _echo("ok: no issues found")
    if errors:
        raise typer.Exit(1)


# --------------------------------------------------------------------------- #
# schema
# --------------------------------------------------------------------------- #
@app.command()
def schema(
    defaults: bool = typer.Option(False, "--defaults", help="List built-in defaults instead."),
) -> None:
    """Emit the JSON Schema for AgentTeam (or built-in defaults)."""
    if defaults:
        _emit_json(kr.builtin_defaults())
    else:
        _emit_json(kr.json_schema())


# --------------------------------------------------------------------------- #
# doctor
# --------------------------------------------------------------------------- #
@app.command()
def doctor() -> None:
    """Environment readiness checks (never mutates anything)."""
    import shutil

    checks: list[tuple[str, str, str]] = []

    def add(name: str, ok: bool, detail: str = "", warn: bool = False) -> None:
        status = "pass" if ok else ("warn" if warn else "fail")
        checks.append((name, status, detail))

    try:
        import claude_agent_sdk  # noqa: F401
        add("claude-agent-sdk", True, "importable")
    except Exception:
        add("claude-agent-sdk", False, "not installed (sdk harness falls back to stub)", warn=True)

    for binary in ("cursor", "codex", "claude"):
        add(f"binary:{binary}", shutil.which(binary) is not None,
            "on PATH" if shutil.which(binary) else "not found (local-only harness)", warn=True)

    profiles = load_profiles()
    add("credential profiles", bool(profiles), f"{len(profiles)} configured" if profiles else "none configured", warn=not profiles)

    if state.json:
        _emit_json([{"check": c, "status": s, "detail": d} for c, s, d in checks])
    else:
        for c, s, d in checks:
            _echo(f"[{s}] {c}: {d}")


# --------------------------------------------------------------------------- #
# run
# --------------------------------------------------------------------------- #
@app.command()
def run(
    objective: Optional[str] = typer.Option(None, "--objective", "-o", help="Objective text."),
    objective_file: Optional[Path] = typer.Option(None, "--objective-file", help="Read objective from a file."),
    team: Optional[Path] = typer.Option(None, "--team", help="A pre-compiled/flat team.yaml."),
    agent: Optional[str] = typer.Option(None, "--agent", help="Dispatch the objective directly to a single agent (skip lead decomposition)."),
    resume: Optional[str] = typer.Option(None, "--resume", help="Resume a run-id."),
    dry_run: bool = typer.Option(False, "--dry-run", help="Plan only, no model calls."),
    max_turns: Optional[int] = typer.Option(None, "--max-turns", help="Cap total turns."),
    autonomy: Optional[str] = typer.Option(None, "--autonomy", help="supervised | autonomous (override)."),
    attach: Optional[str] = typer.Option(None, "--attach", help="Start attached to an agent."),
    detach: bool = typer.Option(False, "--detach", help="Background; return run-id."),
) -> None:
    """Compile → validate → run a team locally (§7)."""
    if objective_file:
        objective = objective_file.read_text("utf-8")
    if not objective and not resume:
        _fail("provide --objective/-o or --objective-file (or --resume)")
    if autonomy and autonomy not in {"supervised", "autonomous"}:
        _fail("--autonomy must be supervised or autonomous")

    result = _compile(team)
    diags = kr.validate(result, target="local")
    errors = [d for d in diags if d.severity == "error"]
    if errors:
        for d in errors:
            _echo(f"{d.file}:{d.line}: error: [{d.code}] {d.message}", err=True)
        _fail("validation failed; fix errors before running")

    if agent:
        names = [a.name for a in result.team.spec.agents]
        if agent not in names:
            _fail(f"unknown agent {agent!r}; team has: {', '.join(names) or '(none)'}")

    project_dir = team.parent if team else state.project
    coord = Coordinator(
        result.team,
        project_dir=project_dir,
        run_id=resume,
        dry_run=dry_run,
        autonomy_override=autonomy,
        max_turns=max_turns,
        target_agent=agent,
        on_event=(None if state.json else _live_event),
    )
    res = asyncio.run(coord.run(objective or ""))

    if state.json:
        _emit_json({
            "run": res.run_id,
            "completed": res.completed,
            "halted": res.halted,
            "halt_reason": res.halt_reason,
            "paused_agents": res.paused_agents,
            "tasks": [t.to_dict() for t in res.tasks],
        })
    else:
        _echo("")
        _echo(f"run {res.run_id}: {'completed' if res.completed else 'incomplete'}")
        if res.halted:
            _echo(f"  halted: {res.halt_reason}")
        if res.paused_agents:
            _echo(f"  paused (attach to steer): {', '.join(res.paused_agents)}")
        for t in res.tasks:
            _echo(f"  - {t.id} [{t.state}] owner={t.owner}")


def _live_event(ev) -> None:
    d = ev.to_dict()
    agent = d.get("agent") or "-"
    typer.echo(f"  · {d['type']:<16} {agent:<14} " + " ".join(
        f"{k}={v}" for k, v in d.items() if k not in {"ts", "type", "run", "agent"}
    ))


# --------------------------------------------------------------------------- #
# ps
# --------------------------------------------------------------------------- #
@app.command()
def ps() -> None:
    """List running agents and their state."""
    store = FileTaskStore(state.project / ".karo" / "tasks")
    tasks = asyncio.run(store.list())
    rows = []
    for t in tasks:
        agent_state = {
            TaskState.in_progress.value: "running",
            TaskState.paused.value: "paused:guard",
            TaskState.pending.value: "idle",
            TaskState.assigned.value: "running",
        }.get(t.state, t.state)
        rows.append({"agent": t.owner, "task": t.id, "state": agent_state})
    if state.json:
        _emit_json(rows)
    elif not rows:
        _echo("no agents/tasks found")
    else:
        for r in rows:
            _echo(f"{r['agent'] or '-':<16} {r['state']:<14} {r['task']}")


# --------------------------------------------------------------------------- #
# tasks
# --------------------------------------------------------------------------- #
tasks_app = typer.Typer(no_args_is_help=True, help="Inspect/manipulate the task layer.")
app.add_typer(tasks_app, name="tasks")


def _task_store() -> FileTaskStore:
    return FileTaskStore(state.project / ".karo" / "tasks")


@tasks_app.command("list")
def tasks_list(task_state: Optional[str] = typer.Option(None, "--state")) -> None:
    items = asyncio.run(_task_store().list(task_state))
    if state.json:
        _emit_json([t.to_dict() for t in items])
    else:
        for t in items:
            _echo(f"{t.id} [{t.state}] owner={t.owner} :: {t.objective[:60]}")


@tasks_app.command("show")
def tasks_show(task_id: str) -> None:
    t = asyncio.run(_task_store().get(task_id))
    if not t:
        _fail(f"task {task_id} not found")
    _emit_json(t.to_dict())


@tasks_app.command("retry")
def tasks_retry(task_id: str) -> None:
    store = _task_store()
    t = asyncio.run(store.get(task_id))
    if not t:
        _fail(f"task {task_id} not found")
    t.state = TaskState.pending.value
    t.owner = None
    t.lease = None
    asyncio.run(store.update(t))
    _echo(f"task {task_id} reset to pending")


@tasks_app.command("cancel")
def tasks_cancel(task_id: str) -> None:
    store = _task_store()
    t = asyncio.run(store.get(task_id))
    if not t:
        _fail(f"task {task_id} not found")
    t.state = TaskState.cancelled.value
    asyncio.run(store.update(t))
    _echo(f"task {task_id} cancelled")


@tasks_app.command("assign")
def tasks_assign(task_id: str, agent: str) -> None:
    store = _task_store()
    t = asyncio.run(store.get(task_id))
    if not t:
        _fail(f"task {task_id} not found")
    t.owner = agent
    asyncio.run(store.update(t))
    _echo(f"task {task_id} assigned to {agent}")


# --------------------------------------------------------------------------- #
# mail
# --------------------------------------------------------------------------- #
mail_app = typer.Typer(no_args_is_help=True, help="Inspect mailboxes.")
app.add_typer(mail_app, name="mail")


def _mail_store() -> FileMailboxStore:
    return FileMailboxStore(state.project / ".karo" / "mail")


@mail_app.command("list")
def mail_list(agent: str) -> None:
    msgs = asyncio.run(_mail_store().inbox(agent))
    if state.json:
        _emit_json([m.to_dict() for m in msgs])
    else:
        for m in msgs:
            flag = " " if m.read else "*"
            _echo(f"{flag}{m.id} from={m.sender} :: {m.body[:60]}")


@mail_app.command("read")
def mail_read(agent: str, msg_id: str) -> None:
    msgs = asyncio.run(_mail_store().inbox(agent))
    for m in msgs:
        if m.id == msg_id:
            asyncio.run(_mail_store().mark_read(agent, msg_id))
            _emit_json(m.to_dict())
            return
    _fail(f"message {msg_id} not found for {agent}")


@mail_app.command("send")
def mail_send(agent: str, body: str = typer.Option(..., "--body")) -> None:
    msg = Message(to=agent, body=body, sender="human")
    asyncio.run(_mail_store().send(msg))
    _echo(f"sent {msg.id} to {agent}")


@mail_app.command("purge")
def mail_purge(agent: str) -> None:
    asyncio.run(_mail_store().purge(agent))
    _echo(f"purged mailbox for {agent}")


# --------------------------------------------------------------------------- #
# memory
# --------------------------------------------------------------------------- #
memory_app = typer.Typer(no_args_is_help=True, help="Inspect/manage memory.")
app.add_typer(memory_app, name="memory")


def _mem_store() -> FileMemoryStore:
    return FileMemoryStore(state.project / ".karo" / "memory")


@memory_app.command("list")
def memory_list(scope: str = typer.Option("team", "--scope")) -> None:
    recs = asyncio.run(_mem_store().query(scope))
    if state.json:
        _emit_json([r.to_dict() for r in recs])
    else:
        for r in recs:
            _echo(f"{r.key} = {str(r.value)[:60]} (tags={r.tags})")


@memory_app.command("get")
def memory_get(key: str, scope: str = typer.Option("team", "--scope")) -> None:
    val = asyncio.run(_mem_store().get(scope, key))
    _emit_json({"key": key, "scope": scope, "value": val})


@memory_app.command("clear")
def memory_clear(scope: str = typer.Option("team", "--scope")) -> None:
    log = state.project / ".karo" / "memory" / f"{scope}.jsonl"
    if log.exists():
        log.unlink()
    _echo(f"cleared memory scope {scope}")


# --------------------------------------------------------------------------- #
# budget
# --------------------------------------------------------------------------- #
budget_app = typer.Typer(no_args_is_help=True, help="Token budget status.")
app.add_typer(budget_app, name="budget")


@budget_app.command("status")
def budget_status() -> None:
    from karo_runtime.runtime import FileBudgetMeter

    result = _compile()
    budgets = result.team.spec.budgets
    meter = FileBudgetMeter(
        state.project / ".karo" / "usage.json",
        limit=budgets.team.limit if budgets and budgets.team else None,
        window=budgets.team.window if budgets and budgets.team else "daily",
    )
    provider = budgets.team.provider if budgets and budgets.team else "anthropic"
    _emit_json(meter.status(provider))


@budget_app.command("reset")
def budget_reset() -> None:
    from karo_runtime.runtime import FileBudgetMeter

    FileBudgetMeter(state.project / ".karo" / "usage.json").reset()
    _echo("budget window reset")


# --------------------------------------------------------------------------- #
# attach
# --------------------------------------------------------------------------- #
@app.command()
def attach(
    agent: str,
    run: Optional[str] = typer.Option(None, "--run", help="Run id."),
    message: Optional[str] = typer.Option(None, "--message", "-m", help="Inject a direction (user turn)."),
    interrupt: bool = typer.Option(False, "--interrupt", help="Interrupt the current turn."),
    continue_: bool = typer.Option(False, "--continue", help="Release a guard-paused agent and hand back to the Coordinator."),
    context: Optional[str] = typer.Option(None, "--context", help="Cluster context (KARO v2 remote)."),
) -> None:
    """Attach to a running agent and direct it — stream + inject + interrupt + detach (§13)."""
    if context:
        _echo(f"[remote] would open a streamed attach session to {agent} on context {context!r}")
        _echo("remote attach targets a KARO v2 cluster (v2 §7); same verbs, streamed transport.")
        return

    result = _compile()
    coord = Coordinator(result.team, project_dir=state.project, run_id=run)

    async def _drive():
        session = await coord.open_attach(agent)
        if interrupt:
            await session.interrupt()
            _echo(f"interrupted {agent}'s current turn")
        if message:
            turn = await session.inject(message)
            _echo(f"[{agent}] {turn.text}")
        # Replay the live transcript (watch the stream).
        async for ev in session.stream():
            d = ev.data
            if ev.type == "human.inject":
                _echo(f"  > you: {d.get('body','')}")
            elif ev.type == "turn.delta":
                _echo(f"  < {agent}: {d.get('text','')}")
        released: list[str] = []
        if continue_:
            released = await coord.release_paused(agent)
        session.detach()
        coord.events.emit("detach", agent=agent, user="local")
        return released

    released = asyncio.run(_drive())
    if continue_:
        if released:
            _echo(f"released {len(released)} paused task(s) for {agent}; re-run `karo run --resume {coord.run_id}` to continue")
        else:
            _echo(f"no guard-paused tasks for {agent}")
    if not (message or interrupt or continue_):
        _echo(f"attached to {agent}; use -m to inject direction, --interrupt, or --continue. (:detach)")


# --------------------------------------------------------------------------- #
# export
# --------------------------------------------------------------------------- #
@app.command()
def export(
    output: Optional[Path] = typer.Option(None, "--output", "-o", help="Output file (default stdout)."),
    format: str = typer.Option("yaml", "--format", help="yaml | json."),
    namespace: Optional[str] = typer.Option(None, "--namespace", help="metadata.namespace."),
    profile: str = typer.Option("prod", "--profile", help="prod | staging."),
    strip_secrets: bool = typer.Option(True, "--strip-secrets/--no-strip-secrets", help="Emit refs, never values."),
    skills_bundle: str = typer.Option("oci", "--skills-bundle", help="oci | git | configmap."),
    allow_local_harness: bool = typer.Option(False, "--allow-local-harness", help="Warn (not reject) on local-only harnesses."),
) -> None:
    """Compile the folder and produce the KARO v2 manifest (§12)."""
    result = _compile()
    try:
        doc, report = export_manifest(
            result.team,
            namespace=namespace,
            profile=profile,
            allow_local_harness=allow_local_harness,
        )
    except ExportError as e:
        _fail(str(e))

    if format == "json":
        text = kr.canonical_json(doc)
    else:
        text = yaml.safe_dump(doc, sort_keys=True, default_flow_style=False)

    if output:
        output.write_text(text, encoding="utf-8")
        _echo(f"wrote {output}")
    else:
        typer.echo(text)

    for t in report.transforms:
        _echo(f"transform: {t}", err=True)
    for w in report.warnings:
        _echo(f"warning: {w}", err=True)
    if report.secrets:
        _echo(f"secrets (refs only): {', '.join(sorted(report.secrets))}", err=True)


# --------------------------------------------------------------------------- #
# secret
# --------------------------------------------------------------------------- #
secret_app = typer.Typer(no_args_is_help=True, help="Manage the local secret store.")
app.add_typer(secret_app, name="secret")


@secret_app.command("set")
def secret_set(name: str, value: str) -> None:
    from .secrets import set_secret

    set_secret(name, value)
    _echo(f"secret {name} set")


@secret_app.command("get")
def secret_get(name: str) -> None:
    from .secrets import get_secret

    val = get_secret(name)
    if val is None:
        _fail(f"secret {name} not found")
    _echo(val)


@secret_app.command("rm")
def secret_rm(name: str) -> None:
    from .secrets import rm_secret

    rm_secret(name)
    _echo(f"secret {name} removed")


# --------------------------------------------------------------------------- #
# version
# --------------------------------------------------------------------------- #
@app.command()
def version() -> None:
    """Print version, build and supported spec apiVersion."""
    info = {
        "karo": kr.__version__,
        "karo_runtime": kr.__version__,
        "spec_apiVersion": kr.SPEC_API_VERSION,
        "python": sys.version.split()[0],
    }
    if state.json:
        _emit_json(info)
    else:
        for k, v in info.items():
            _echo(f"{k}: {v}")


if __name__ == "__main__":
    app()
