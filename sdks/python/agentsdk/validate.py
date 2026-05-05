"""validate_envelope — protocol validation for JSONL envelopes.

Checks envelopes against the JSONL protocol rules.  Each envelope type has
required and forbidden fields that mirror the constructor isolation pattern
used on the producer side.
"""

from __future__ import annotations

from dataclasses import asdict
from typing import Any, Union

from agentsdk.envelope import (
    TYPE_ERROR,
    TYPE_PROGRESS,
    TYPE_RESULT,
    TYPE_WARNING,
    Envelope,
)

# Required non-empty string fields on every envelope.
_COMMON_REQUIRED_FIELDS = ("version", "tool", "timestamp")

# The set of known envelope types.
_KNOWN_TYPES = {TYPE_RESULT, TYPE_ERROR, TYPE_WARNING, TYPE_PROGRESS}

# Per-type rules: (required_fields, forbidden_fields).
# required_fields: field names that must be present and non-falsy.
# forbidden_fields: field names that must be absent or None/0.
_TYPE_RULES: dict[str, tuple[tuple[str, ...], tuple[str, ...]]] = {
    TYPE_RESULT: (("data",), ("error_code", "percent")),
    TYPE_ERROR: (("error_code", "message"), ("data", "percent")),
    TYPE_WARNING: (("message",), ("data", "error_code", "percent")),
    TYPE_PROGRESS: (("percent",), ("data", "error_code")),
}


def _field_value(d: dict[str, Any], field: str) -> Any:
    """Return the raw value for *field*, treating absent keys as None."""
    return d.get(field, None)


def _is_present(value: Any) -> bool:
    """Return True if *value* counts as 'present' for validation.

    - None → absent
    - "" (empty string) → absent
    - 0 (zero int) → absent (matches Envelope.to_dict() omitempty)
    - Any other value → present
    """
    if value is None:
        return False
    if isinstance(value, str) and value == "":
        return False
    if isinstance(value, int) and value == 0:
        return False
    return True


def validate_envelope(envelope: Union[dict, Envelope]) -> None:
    """Validate *envelope* against JSONL protocol rules.

    Returns ``None`` on success.  Raises ``ValueError`` with a descriptive
    message on failure.

    Accepts both a plain ``dict`` and an ``Envelope`` dataclass instance.
    """
    # Normalise to dict.
    if isinstance(envelope, Envelope):
        d: dict[str, Any] = asdict(envelope)
    elif isinstance(envelope, dict):
        d = envelope
    else:
        raise ValueError(f"envelope must be dict or Envelope, got {type(envelope).__name__}")

    # --- Common required string fields ---
    for field in _COMMON_REQUIRED_FIELDS:
        value = d.get(field)
        if not isinstance(value, str) or value == "":
            raise ValueError(f"missing required field: {field}")

    # --- Type must be known ---
    env_type = d.get("type")
    if not isinstance(env_type, str) or env_type not in _KNOWN_TYPES:
        raise ValueError(
            f"invalid type: {env_type!r} (expected one of {sorted(_KNOWN_TYPES)})"
        )

    # --- Type-specific rules ---
    required_fields, forbidden_fields = _TYPE_RULES[env_type]

    # Check required fields are present.
    for field in required_fields:
        value = _field_value(d, field)
        if not _is_present(value):
            raise ValueError(f"{env_type} envelope missing required field: {field}")

    # Check forbidden fields are absent.
    for field in forbidden_fields:
        value = _field_value(d, field)
        if _is_present(value):
            raise ValueError(f"{env_type} envelope has forbidden field: {field}")

    # --- Progress percent range check (0–100 inclusive) ---
    if env_type == TYPE_PROGRESS:
        percent = d.get("percent")
        if percent is not None and (not isinstance(percent, (int, float)) or percent < 0 or percent > 100):
            raise ValueError(f"progress percent must be 0-100, got {percent}")
