"""Folder-compile == flat-team.yaml round-trip parity (§4.0.1).

§4.0.1 claims a folder and the equivalent hand-written flat ``team.yaml`` produce
equivalent output *after canonicalization*. This guards the documented
equivalence and the frontmatter-body vs ``instructions: |`` normalization (the
plausible break: AGENT.md bodies are stripped while flat block scalars carry a
trailing newline).
"""

import textwrap
from pathlib import Path

from karo_runtime.spec import compile_flat, compile_folder, parity_projection


def test_folder_and_flat_are_canonically_equivalent(tmp_path: Path):
    # --- folder form ---
    folder = tmp_path / "folder"
    (folder / "agents" / "planner").mkdir(parents=True)
    (folder / "agents" / "worker").mkdir(parents=True)
    (folder / "karo.yaml").write_text(
        textwrap.dedent(
            """\
            apiVersion: karo.dev/v1
            kind: AgentTeam
            metadata:
              name: crew
            spec:
              defaults:
                harness: sdk
                model: { provider: anthropic, id: claude-opus-4-8 }
              coordination:
                pattern: lead-and-teammates
                lead: planner
              agents:
                - { ref: agents/planner }
                - { ref: agents/worker }
            """
        )
    )
    (folder / "agents" / "planner" / "AGENT.md").write_text(
        "---\nname: planner\nharness: sdk\n---\nYou are the lead.\n"
    )
    (folder / "agents" / "worker" / "AGENT.md").write_text(
        "---\nname: worker\nharness: sdk\n---\nYou implement.\n"
    )

    # --- equivalent flat form ---
    flat = tmp_path / "team.yaml"
    flat.write_text(
        textwrap.dedent(
            """\
            apiVersion: karo.dev/v1
            kind: AgentTeam
            metadata:
              name: crew
            spec:
              defaults:
                harness: sdk
                model: { provider: anthropic, id: claude-opus-4-8 }
              coordination:
                pattern: lead-and-teammates
                lead: planner
              agents:
                - name: planner
                  harness: sdk
                  instructions: You are the lead.
                - name: worker
                  harness: sdk
                  instructions: You implement.
            """
        )
    )

    folder_proj = parity_projection(compile_folder(folder).team)
    flat_proj = parity_projection(compile_flat(flat).team)
    assert folder_proj == flat_proj
