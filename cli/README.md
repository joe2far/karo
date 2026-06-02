# karo (CLI)

The `karo` command. Install with pipx (once published) or from this monorepo:

```bash
pip install -e karo-runtime -e cli
karo --help
```

Common commands:

```bash
karo init --name my-team --template lead-team   # scaffold a §4.0 folder
karo validate                                   # static checks, no network
karo compile -o team.yaml                       # folder -> compiled AgentTeam
karo run -o "ship the feature"                  # run the whole team
karo run reviewer "review the auth changes"     # sling at one agent (positional)
karo sling pm-team/deploy-approver "approve X"  # fire at team/agent (folder or, with --context, namespace)
karo run -o "..." --dry-run                     # plan only, no model calls
karo export -o manifest.yaml --namespace agents # KARO v2 handoff
karo schema                                     # JSON Schema for editors
```

See `docs/PRD-KARO-CLI.md` for the full command reference (§7).
