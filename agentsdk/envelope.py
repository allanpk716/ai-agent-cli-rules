"""Envelope — the core JSONL protocol wrapper.

Each envelope type uses a classmethod constructor that only sets allowed fields.
Serialization via to_json() excludes None-valued strings and zero-valued integers,
matching the Go implementation's omitempty semantics.
"""

from __future__ import annotations

import json
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from typing import Any, Optional

# Protocol version shared by all envelopes.
ENVELOPE_VERSION = "1.0"

# Envelope type constants.
TYPE_RESULT = "result"
TYPE_ERROR = "error"
TYPE_WARNING = "warning"
TYPE_PROGRESS = "progress"


def _utc_now_rfc3339() -> str:
    """Return the current UTC time as an RFC3339 string."""
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


@dataclass
class Envelope:
    """Top-level JSONL protocol envelope.

    Field exclusion is enforced via constructors — only the fields appropriate
    for each type are set. ``to_json()`` serializes, omitting None / 0 values
    to match the Go ``omitempty`` behaviour.
    """

    version: str = ENVELOPE_VERSION
    tool: str = ""
    type: str = ""
    timestamp: str = field(default_factory=_utc_now_rfc3339)
    data: Optional[Any] = None
    error_code: Optional[str] = None
    message: Optional[str] = None
    percent: int = 0
    trace_id: Optional[str] = None

    # ------------------------------------------------------------------
    # Constructors (one per envelope type)
    # ------------------------------------------------------------------

    @classmethod
    def result(cls, *, tool: str, data: Any) -> Envelope:
        """Create a result envelope — carries ``data`` only."""
        return cls(tool=tool, type=TYPE_RESULT, data=data)

    @classmethod
    def error(cls, *, tool: str, error_code: str, message: str) -> Envelope:
        """Create an error envelope — carries ``error_code`` + ``message``."""
        return cls(tool=tool, type=TYPE_ERROR, error_code=error_code, message=message)

    @classmethod
    def warning(cls, *, tool: str, message: str) -> Envelope:
        """Create a warning envelope — carries ``message`` only."""
        return cls(tool=tool, type=TYPE_WARNING, message=message)

    @classmethod
    def progress(cls, *, tool: str, percent: int, message: str) -> Envelope:
        """Create a progress envelope — carries ``percent`` + ``message``."""
        return cls(tool=tool, type=TYPE_PROGRESS, percent=percent, message=message)

    # ------------------------------------------------------------------
    # Serialization
    # ------------------------------------------------------------------

    def to_dict(self) -> dict[str, Any]:
        """Convert to a dict, excluding None strings and zero ints.

        This mirrors Go's ``omitempty`` JSON tag semantics:
        - ``None`` string/Any fields → omitted
        - ``0`` int fields (percent) → omitted
        - ``""`` empty string fields (version/tool/type/timestamp) are kept
          because they are structurally required.
        """
        d: dict[str, Any] = {}
        for key, value in asdict(self).items():
            if value is None:
                continue
            if isinstance(value, int) and value == 0:
                continue
            d[key] = value
        return d

    def to_json(self) -> str:
        """Serialize to a JSON string, excluding zero-valued fields."""
        return json.dumps(self.to_dict(), separators=(",", ":"))
