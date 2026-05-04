"""Tests for health check types."""

from __future__ import annotations

from agentsdk.health import HealthCheckResult


class TestHealthCheckResult:
    def test_creation_with_all_fields(self) -> None:
        result = HealthCheckResult(
            name="sandbox_writable",
            status="pass",
            message="Sandbox is writable",
            details={"path": "/tmp/test"},
        )
        assert result.name == "sandbox_writable"
        assert result.status == "pass"
        assert result.message == "Sandbox is writable"
        assert result.details == {"path": "/tmp/test"}

    def test_creation_minimal(self) -> None:
        result = HealthCheckResult(name="db_conn", status="fail")
        assert result.message == ""
        assert result.details is None

    def test_to_dict_includes_all_when_present(self) -> None:
        result = HealthCheckResult(
            name="check",
            status="warning",
            message="slow",
            details={"latency_ms": 500},
        )
        d = result.to_dict()
        assert d == {
            "name": "check",
            "status": "warning",
            "message": "slow",
            "details": {"latency_ms": 500},
        }

    def test_to_dict_omits_empty_message(self) -> None:
        result = HealthCheckResult(name="check", status="pass")
        d = result.to_dict()
        assert "message" not in d

    def test_to_dict_omits_none_details(self) -> None:
        result = HealthCheckResult(name="check", status="pass")
        d = result.to_dict()
        assert "details" not in d

    def test_to_dict_includes_non_empty_message(self) -> None:
        result = HealthCheckResult(name="check", status="fail", message="error")
        d = result.to_dict()
        assert d["message"] == "error"

    def test_all_statuses(self) -> None:
        for status in ("pass", "fail", "warning"):
            result = HealthCheckResult(name="check", status=status)
            assert result.status == status
            assert result.to_dict()["status"] == status
