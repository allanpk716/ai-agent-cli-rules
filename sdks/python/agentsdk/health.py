"""Health check types for agent doctor commands.

Defines the status literal, result dataclass, and function signature
used by health check registrations.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable, Dict, Literal, Optional

HealthCheckStatus = Literal["pass", "fail", "warning"]
"""Possible outcomes of a single health check."""


@dataclass
class HealthCheckResult:
    """Outcome of a single health check.

    Parameters
    ----------
    name:
        Human-readable check name (e.g. ``"sandbox_writable"``).
    status:
        One of ``"pass"``, ``"fail"``, or ``"warning"``.
    message:
        Optional detail message.  Omitted from serialization when empty.
    details:
        Optional structured details dict.  Omitted from serialization when ``None``.
    """

    name: str
    status: HealthCheckStatus
    message: str = ""
    details: Optional[Dict[str, Any]] = field(default=None)

    def to_dict(self) -> Dict[str, Any]:
        """Serialize to a dict, omitting empty/None optional fields."""
        d: Dict[str, Any] = {
            "name": self.name,
            "status": self.status,
        }
        if self.message:
            d["message"] = self.message
        if self.details is not None:
            d["details"] = self.details
        return d


HealthCheckFunc = Callable[[], HealthCheckResult]
"""Signature for a function that performs a single health check."""
