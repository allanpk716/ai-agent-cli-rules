"""Tests for ConfigManager[T] — Pydantic introspection, redaction, atomic save."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Optional

import pytest
from pydantic import BaseModel, Field, field_validator

from agentsdk.config import ConfigManager, ConfigProvider


# ---------------------------------------------------------------------------
# Test Pydantic models
# ---------------------------------------------------------------------------

class _TestConfig(BaseModel):
    """Sample config with sensitive, configurable, and internal fields."""

    name: str = Field(default="default", json_schema_extra={"config": True})
    api_key: str = Field(default="", json_schema_extra={"sensitive": True})
    port: int = Field(default=8080, json_schema_extra={"config": True})
    rate: float = Field(default=1.0, json_schema_extra={"config": True})
    debug: bool = Field(default=False, json_schema_extra={"config": True})
    internal: str = Field(default="hidden")  # no config, no sensitive


class _SensitiveTypesConfig(BaseModel):
    """Config with non-string sensitive fields to test type-preserving redaction."""

    secret_int: int = Field(default=42, json_schema_extra={"sensitive": True})
    secret_bool: bool = Field(default=True, json_schema_extra={"sensitive": True})
    secret_str: str = Field(default="hunter2", json_schema_extra={"sensitive": True})
    normal: str = Field(default="visible")


class _AliasedConfig(BaseModel):
    """Config with field aliases."""

    display_name: str = Field(default="test", alias="displayName", json_schema_extra={"config": True})


class _ValidatedConfig(BaseModel):
    """Config with custom Pydantic validator."""

    port: int = Field(default=8080, json_schema_extra={"config": True})

    @field_validator("port")
    @classmethod
    def port_range(cls, v: int) -> int:
        if not (1 <= v <= 65535):
            raise ValueError("port must be between 1 and 65535")
        return v


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def config_dir(tmp_path: Path) -> Path:
    """Return a temporary directory for config files."""
    return tmp_path


@pytest.fixture
def config_file(config_dir: Path) -> str:
    """Return a path to a test config file with default data."""
    path = config_dir / "config.json"
    path.write_text(
        json.dumps({
            "name": "test-app",
            "api_key": "super-secret",
            "port": 9090,
            "rate": 2.5,
            "debug": True,
            "internal": "sys-value",
        }),
        encoding="utf-8",
    )
    return str(path)


@pytest.fixture
def mgr(config_file: str) -> ConfigManager[_TestConfig]:
    """Return a ConfigManager for _TestConfig."""
    return ConfigManager(_TestConfig, config_file)


# ---------------------------------------------------------------------------
# Load / Save round-trip
# ---------------------------------------------------------------------------

class TestLoadSave:
    def test_load_returns_valid_model(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = mgr.load()
        assert isinstance(cfg, _TestConfig)
        assert cfg.name == "test-app"
        assert cfg.port == 9090

    def test_save_and_reload_round_trip(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = mgr.load()
        cfg.name = "updated"
        mgr.save(cfg)

        reloaded = mgr.load()
        assert reloaded.name == "updated"

    def test_atomic_save_no_tmp_leftover(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = mgr.load()
        mgr.save(cfg)
        assert not os.path.exists(mgr.file_path + ".tmp")

    def test_save_creates_parent_dirs(self, config_dir: Path) -> None:
        nested = config_dir / "a" / "b" / "config.json"
        m = ConfigManager(_TestConfig, str(nested))
        cfg = _TestConfig()
        m.save(cfg)
        assert nested.exists()

    def test_load_missing_file_raises(self, config_dir: Path) -> None:
        m = ConfigManager(_TestConfig, str(config_dir / "missing.json"))
        with pytest.raises(FileNotFoundError):
            m.load()

    def test_load_malformed_json_raises(self, config_dir: Path) -> None:
        bad = config_dir / "bad.json"
        bad.write_text("{invalid", encoding="utf-8")
        m = ConfigManager(_TestConfig, str(bad))
        with pytest.raises(json.JSONDecodeError):
            m.load()


# ---------------------------------------------------------------------------
# Redaction
# ---------------------------------------------------------------------------

class TestRedaction:
    def test_redacted_masks_sensitive_string(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = mgr.load()
        redacted = mgr.redacted(cfg)
        assert redacted.api_key == "***"
        assert redacted.name == "test-app"  # non-sensitive unchanged

    def test_redacted_preserves_non_sensitive(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = mgr.load()
        redacted = mgr.redacted(cfg)
        assert redacted.port == 9090
        assert redacted.debug is True
        assert redacted.internal == "sys-value"

    def test_redacted_does_not_modify_original(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = mgr.load()
        mgr.redacted(cfg)
        assert cfg.api_key == "super-secret"

    def test_redacted_non_string_types(self, config_dir: Path) -> None:
        path = config_dir / "sensitive_types.json"
        path.write_text("{}", encoding="utf-8")
        m = ConfigManager(_SensitiveTypesConfig, str(path))
        cfg = _SensitiveTypesConfig()
        redacted = m.redacted(cfg)
        # All sensitive fields redacted regardless of type (MEM039)
        assert redacted.secret_int == "***"
        assert redacted.secret_bool == "***"
        assert redacted.secret_str == "***"
        assert redacted.normal == "visible"

    def test_redacted_config_with_only_sensitive_fields(self, config_dir: Path) -> None:
        class AllSecret(BaseModel):
            key1: str = Field(default="a", json_schema_extra={"sensitive": True})
            key2: int = Field(default=1, json_schema_extra={"sensitive": True})

        path = config_dir / "all_secret.json"
        path.write_text("{}", encoding="utf-8")
        m = ConfigManager(AllSecret, str(path))
        redacted = m.redacted(AllSecret())
        assert redacted.key1 == "***"
        assert redacted.key2 == "***"


# ---------------------------------------------------------------------------
# set_by_path — type conversion and validation
# ---------------------------------------------------------------------------

class TestSetByPath:
    def test_set_string_field(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        mgr.set_by_path(cfg, "name", "new-name")
        assert cfg.name == "new-name"

    def test_set_int_field(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        mgr.set_by_path(cfg, "port", "3000")
        assert cfg.port == 3000

    def test_set_float_field(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        mgr.set_by_path(cfg, "rate", "3.14")
        assert cfg.rate == pytest.approx(3.14)

    def test_set_bool_field_true(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        mgr.set_by_path(cfg, "debug", "true")
        assert cfg.debug is True

    def test_set_bool_field_false(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig(debug=True)
        mgr.set_by_path(cfg, "debug", "false")
        assert cfg.debug is False

    def test_set_bool_alternate_values(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        mgr.set_by_path(cfg, "debug", "yes")
        assert cfg.debug is True

        mgr.set_by_path(cfg, "debug", "0")
        assert cfg.debug is False

    def test_rejects_non_whitelisted_field(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        with pytest.raises(ValueError, match="not user-configurable"):
            mgr.set_by_path(cfg, "api_key", "new-key")

    def test_rejects_unknown_field(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        with pytest.raises(ValueError, match="unknown config field"):
            mgr.set_by_path(cfg, "nonexistent", "value")

    def test_rejects_internal_field(self, mgr: ConfigManager[_TestConfig]) -> None:
        cfg = _TestConfig()
        with pytest.raises(ValueError, match="not user-configurable"):
            mgr.set_by_path(cfg, "internal", "new-value")


# ---------------------------------------------------------------------------
# Whitelist
# ---------------------------------------------------------------------------

class TestWhitelist:
    def test_whitelist_returns_configurable_fields(self, mgr: ConfigManager[_TestConfig]) -> None:
        wl = mgr.whitelist()
        assert "name" in wl
        assert "port" in wl
        assert "rate" in wl
        assert "debug" in wl

    def test_whitelist_excludes_sensitive(self, mgr: ConfigManager[_TestConfig]) -> None:
        wl = mgr.whitelist()
        assert "api_key" not in wl

    def test_whitelist_excludes_internal(self, mgr: ConfigManager[_TestConfig]) -> None:
        wl = mgr.whitelist()
        assert "internal" not in wl


# ---------------------------------------------------------------------------
# ConfigProvider protocol
# ---------------------------------------------------------------------------

class TestConfigProvider:
    def test_config_manager_satisfies_protocol(self, mgr: ConfigManager[_TestConfig]) -> None:
        assert isinstance(mgr, ConfigProvider)

    def test_list_redacted_adapter(self, mgr: ConfigManager[_TestConfig]) -> None:
        result = mgr.list_redacted()
        assert isinstance(result, dict)
        assert result["api_key"] == "***"
        assert result["name"] == "test-app"

    def test_set_adapter(self, mgr: ConfigManager[_TestConfig]) -> None:
        mgr.set("name", "via-adapter")
        reloaded = mgr.load()
        assert reloaded.name == "via-adapter"


# ---------------------------------------------------------------------------
# Aliased fields
# ---------------------------------------------------------------------------

class TestAliases:
    def test_aliased_field_json_name(self, config_dir: Path) -> None:
        path = config_dir / "alias.json"
        path.write_text('{"displayName": "hello"}', encoding="utf-8")
        m = ConfigManager(_AliasedConfig, str(path))
        cfg = m.load()
        assert cfg.display_name == "hello"

    def test_aliased_field_whitelist_uses_json_name(self, config_dir: Path) -> None:
        path = config_dir / "alias.json"
        path.write_text("{}", encoding="utf-8")
        m = ConfigManager(_AliasedConfig, str(path))
        assert "displayName" in m.whitelist()


# ---------------------------------------------------------------------------
# Pydantic validator integration
# ---------------------------------------------------------------------------

class TestValidatorIntegration:
    def test_validate_passes_for_valid(self, config_dir: Path) -> None:
        path = config_dir / "validated.json"
        path.write_text('{"port": 8080}', encoding="utf-8")
        m = ConfigManager(_ValidatedConfig, str(path))
        cfg = m.load()
        m.validate(cfg)  # should not raise

    def test_validate_fails_for_invalid(self, config_dir: Path) -> None:
        from pydantic import ValidationError

        path = config_dir / "validated.json"
        path.write_text('{"port": 8080}', encoding="utf-8")
        m = ConfigManager(_ValidatedConfig, str(path))
        cfg = m.load()
        # Manually set an invalid port (bypassing validator)
        object.__setattr__(cfg, "port", 99999)
        with pytest.raises(ValidationError, match="port must be between"):
            m.validate(cfg)

    def test_set_adapter_runs_validation(self, config_dir: Path) -> None:
        from pydantic import ValidationError

        path = config_dir / "validated.json"
        path.write_text('{"port": 8080}', encoding="utf-8")
        m = ConfigManager(_ValidatedConfig, str(path))
        with pytest.raises(ValidationError, match="port must be between"):
            m.set("port", "99999")


# ---------------------------------------------------------------------------
# Boundary: empty config
# ---------------------------------------------------------------------------

class TestBoundaryConditions:
    def test_empty_config_file_uses_defaults(self, config_dir: Path) -> None:
        path = config_dir / "empty.json"
        path.write_text("{}", encoding="utf-8")
        m = ConfigManager(_TestConfig, str(path))
        cfg = m.load()
        assert cfg.name == "default"
        assert cfg.port == 8080
