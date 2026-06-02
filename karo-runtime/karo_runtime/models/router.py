"""Model router — maps ``model.provider/id(/profile)`` to a concrete client.

Identical code on CLI and cluster (v2 §8). Credential resolution order (§4.5,
§14): explicit ``model.profile`` → first profile matching ``model.provider`` →
error. ``model.profile`` is a *local* selection mechanism; the operator ignores
it and uses IRSA / Workload Identity / mounted Secrets instead.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional


@dataclass
class CredentialProfile:
    name: str
    provider: str
    config: dict[str, Any] = field(default_factory=dict)


@dataclass
class ResolvedModel:
    provider: str
    id: str
    profile: Optional[CredentialProfile]
    params: dict[str, Any] = field(default_factory=dict)


class ModelRouter:
    def __init__(self, profiles: Optional[dict[str, CredentialProfile]] = None):
        self.profiles = profiles or {}

    def resolve(self, model: dict[str, Any], *, require_profile: bool = False) -> ResolvedModel:
        provider = model.get("provider", "anthropic")
        model_id = model.get("id")
        if not model_id:
            raise ValueError("model.id is required")
        requested = model.get("profile")

        profile: Optional[CredentialProfile] = None
        if requested:
            profile = self.profiles.get(requested)
            if profile is None and require_profile:
                raise ValueError(f"credential profile {requested!r} not found")
        else:
            # First profile matching the provider.
            for p in self.profiles.values():
                if p.provider == provider:
                    profile = p
                    break

        if require_profile and profile is None:
            raise ValueError(
                f"no credential profile resolves for provider {provider!r}; "
                f"configure one in ~/.config/karo/config.yaml (§14)"
            )

        return ResolvedModel(
            provider=provider,
            id=model_id,
            profile=profile,
            params=model.get("params", {}),
        )
