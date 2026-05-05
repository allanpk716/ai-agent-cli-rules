"""ConfigManager[T] — generic configuration manager with Pydantic introspection.

Provides load/save/validate/redact/set-by-path/whitelist operations on any
Pydantic BaseModel subclass.  Field metadata (sensitive, configurable) is
computed once at construction from ``Field(json_schema_extra=...)`` and cached.

Also defines the ``ConfigProvider`` protocol that bridges the generic
``ConfigManager[T]`` to non-generic agent commands.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Generic, Literal, Protocol, TypeVar, get_args, get_origin, runtime_checkable

from pydantic import BaseModel
from pydantic.fields import FieldInfo

T = TypeVar("T", bound=BaseModel)


# ---------------------------------------------------------------------------
# Field metadata cache
# ---------------------------------------------------------------------------

class _FieldMeta:
    """Cached metadata for a single Pydantic model field."""

    __slots__ = ("json_name", "pydantic_name", "sensitive", "configurable", "field_type")

    def __init__(
        self,
        json_name: str,
        pydantic_name: str,
        sensitive: bool,
        configurable: bool,
        field_type: type,
    ) -> None:
        self.json_name = json_name
        self.pydantic_name = pydantic_name
        self.sensitive = sensitive
        self.configurable = configurable
        self.field_type = field_type


def _extract_extra_bool(field_info: FieldInfo, key: str) -> bool:
    """Extract a boolean flag from ``Field(json_schema_extra={key: ...})``."""
    extra = field_info.json_schema_extra
    if isinstance(extra, dict):
        return bool(extra.get(key, False))
    return False


def _unwrap_type(annotation: Any) -> type:
    """Return the concrete type behind Optional[X], Union[X, None], etc."""
    origin = get_origin(annotation)
    if origin is Literal:
        return str  # Literal values — treat as str for conversion
    if origin is not None:
        args = get_args(annotation)
        non_none = [a for a in args if a is not type(None)]
        if non_none:
            return _unwrap_type(non_none[0])
    if isinstance(annotation, type):
        return annotation
    return str  # fallback


# ---------------------------------------------------------------------------
# ConfigProvider protocol
# ---------------------------------------------------------------------------

@runtime_checkable
class ConfigProvider(Protocol):
    """Interface that decouples agent commands from ConfigManager[T]'s generic parameter."""

    def list_redacted(self) -> Any:
        """Load config and return a redacted copy."""
        ...

    def set(self, json_path: str, value: str) -> None:
        """Set a config value by JSON path and persist."""
        ...

    def whitelist(self) -> list[str]:
        """Return JSON paths of all user-configurable fields."""
        ...


# ---------------------------------------------------------------------------
# ConfigManager[T]
# ---------------------------------------------------------------------------

class ConfigManager(Generic[T]):
    """Generic configuration manager backed by a JSON file and a Pydantic model.

    Type parameter ``T`` must be a concrete ``pydantic.BaseModel`` subclass.
    Field metadata (sensitive / configurable flags) is introspected once at
    construction and cached for the lifetime of the instance.

    Parameters
    ----------
    model_class:
        The Pydantic BaseModel subclass to use for validation.
    file_path:
        Path to the JSON configuration file.
    """

    def __init__(self, model_class: type[T], file_path: str) -> None:
        self._model_class = model_class
        self._file_path = file_path
        self._fields: dict[str, _FieldMeta] = {}
        self._introspect_fields()

    # -- Public properties ---------------------------------------------------

    @property
    def file_path(self) -> str:
        """Return the configuration file path."""
        return self._file_path

    # -- Introspection -------------------------------------------------------

    def _introspect_fields(self) -> None:
        """Iterate T.model_fields and cache field metadata."""
        for pydantic_name, field_info in self._model_class.model_fields.items():
            # JSON name: alias takes priority, then pydantic name
            json_name = field_info.alias or pydantic_name

            sensitive = _extract_extra_bool(field_info, "sensitive")
            configurable = _extract_extra_bool(field_info, "config")

            # Determine the concrete type for value conversion
            field_type = _unwrap_type(field_info.annotation)

            meta = _FieldMeta(
                json_name=json_name,
                pydantic_name=pydantic_name,
                sensitive=sensitive,
                configurable=configurable,
                field_type=field_type,
            )
            self._fields[json_name] = meta

    # -- Core operations -----------------------------------------------------

    def load(self) -> T:
        """Read the JSON file and parse into T.

        Raises:
            FileNotFoundError: If the config file does not exist.
            json.JSONDecodeError: If the file contains invalid JSON.
            pydantic.ValidationError: If the data does not satisfy T's schema.
        """
        raw = Path(self._file_path).read_text(encoding="utf-8")
        data = json.loads(raw)
        return self._model_class.model_validate(data)

    def save(self, cfg: T) -> None:
        """Atomically write *cfg* to the configuration file.

        Uses a write-to-tmp + ``os.replace`` pattern (same as ``crashdump.py``)
        to avoid partial files on crash.
        """
        data = cfg.model_dump(mode="json")
        payload = json.dumps(data, indent=2)

        final_path = Path(self._file_path)
        final_path.parent.mkdir(parents=True, exist_ok=True)

        tmp_path = str(final_path) + ".tmp"
        try:
            with open(tmp_path, "w", encoding="utf-8") as f:
                f.write(payload)
            os.replace(tmp_path, str(final_path))
        except BaseException:
            # Clean up tmp file on any failure (including cancellation)
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            raise

    def validate(self, cfg: T) -> None:
        """Run Pydantic validation on *cfg*.

        Re-validates via ``model_validate(model_dump())`` to trigger all
        Pydantic validators (including custom ones).

        Raises:
            pydantic.ValidationError: If validation fails.
        """
        data = cfg.model_dump(mode="json")
        self._model_class.model_validate(data)

    def redacted(self, cfg: T) -> T:
        """Return a deep copy with sensitive fields replaced by ``"***"``.

        Unlike the Go SDK which only redacts strings, this implementation
        redacts sensitive fields of *any* type (str, int, bool, etc.) per MEM039.
        """
        copy = cfg.model_copy(deep=True)
        for json_name, meta in self._fields.items():
            if meta.sensitive:
                # Use pydantic name for attribute access
                object.__setattr__(copy, meta.pydantic_name, "***")
        return copy

    def set_by_path(self, cfg: T, json_path: str, value: str) -> None:
        """Set a field on *cfg* by its JSON name after validation.

        Converts the string *value* to the field's native type.  Only
        whitelisted (configurable) fields may be set.

        Raises:
            ValueError: If *json_path* is not in the whitelist or is unknown.
            ValueError: If the string value cannot be converted to the field type.
        """
        if json_path not in self._fields:
            raise ValueError(f"unknown config field: {json_path!r}")

        meta = self._fields[json_path]
        if not meta.configurable:
            raise ValueError(
                f"field {json_path!r} is not user-configurable"
            )

        converted = self._convert_value(value, meta.field_type)
        object.__setattr__(cfg, meta.pydantic_name, converted)

    def whitelist(self) -> list[str]:
        """Return JSON names of all fields marked as user-configurable."""
        return [
            json_name
            for json_name, meta in self._fields.items()
            if meta.configurable
        ]

    # -- ConfigProvider adapter methods --------------------------------------

    def list_redacted(self) -> Any:
        """Load config and return a redacted copy (ConfigProvider adapter)."""
        cfg = self.load()
        redacted_cfg = self.redacted(cfg)
        return redacted_cfg.model_dump(mode="json")

    def set(self, json_path: str, value: str) -> None:
        """Load, set a field, validate, and save (ConfigProvider adapter)."""
        cfg = self.load()
        self.set_by_path(cfg, json_path, value)
        self.validate(cfg)
        self.save(cfg)

    # -- Internal helpers ----------------------------------------------------

    @staticmethod
    def _convert_value(value: str, target_type: type) -> Any:
        """Convert a string value to *target_type*.

        Supported types: ``str``, ``int``, ``float``, ``bool``.
        """
        if target_type is bool:
            # "true"/"1" → True, "false"/"0" → False (case-insensitive)
            lower = value.lower()
            if lower in ("true", "1", "yes"):
                return True
            if lower in ("false", "0", "no"):
                return False
            raise ValueError(f"cannot convert {value!r} to bool")
        if target_type is int:
            return int(value)
        if target_type is float:
            return float(value)
        # Default: treat as string
        return value
