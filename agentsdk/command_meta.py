"""CommandMeta — metadata for agent command registration."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass
class CommandMeta:
    """Metadata about an agent command, used for schema generation.

    Parameters
    ----------
    description:
        Human-readable description of the command.
    is_idempotent:
        Whether the command is safe to retry without side effects.
    """

    description: str = ""
    is_idempotent: bool = False
