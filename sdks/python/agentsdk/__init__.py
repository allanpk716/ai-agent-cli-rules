"""agentsdk — Python SDK for the AI Agent JSONL protocol."""

# S01 — Envelope protocol types
from agentsdk.envelope import (
    ENVELOPE_VERSION,
    TYPE_ERROR,
    TYPE_PROGRESS,
    TYPE_RESULT,
    TYPE_WARNING,
    Envelope,
)
from agentsdk.validate import validate_envelope

# S02 — Runtime core
from agentsdk.app import App
from agentsdk.sandbox import Sandbox
from agentsdk.flightcontext import FlightContext
from agentsdk.crashdump import CrashDump, write_crash_dump
from agentsdk.signalhandler import setup_signal_handler, SignalHandlerConfig
from agentsdk.writer import Writer
from agentsdk.exitcode import (
    ExitError,
    ErrorCodeRegistry,
    EXIT_SUCCESS,
    EXIT_FATAL_ERROR,
    EXIT_INVALID_PARAMS,
    EXIT_NOT_FOUND,
    EXIT_NETWORK_ERROR,
    EXIT_LOCK_CONFLICT,
)

# S03 — ConfigManager + agent meta-commands
from agentsdk.config import ConfigManager, ConfigProvider
from agentsdk.health import HealthCheckStatus, HealthCheckResult, HealthCheckFunc
from agentsdk.command_meta import CommandMeta
from agentsdk.agent_commands import create_agent_app

# S03 — Backup module
from agentsdk.backup import (
    BackupConfig,
    BackupMeta,
    CreateBackup,
    DefaultBackupConfig,
    GFSRotate,
    ListBackups,
    LoadBackupConfig,
    RetentionPolicy,
    RotationResult,
    SaveBackupConfig,
)

__all__ = [
    # S01
    "ENVELOPE_VERSION",
    "TYPE_ERROR",
    "TYPE_PROGRESS",
    "TYPE_RESULT",
    "TYPE_WARNING",
    "Envelope",
    "validate_envelope",
    # S02 — Runtime
    "App",
    "Sandbox",
    "FlightContext",
    "CrashDump",
    "write_crash_dump",
    "setup_signal_handler",
    "SignalHandlerConfig",
    "Writer",
    "ExitError",
    "ErrorCodeRegistry",
    "EXIT_SUCCESS",
    "EXIT_FATAL_ERROR",
    "EXIT_INVALID_PARAMS",
    "EXIT_NOT_FOUND",
    "EXIT_NETWORK_ERROR",
    "EXIT_LOCK_CONFLICT",
    # S03 — ConfigManager + agent meta-commands
    "ConfigManager",
    "ConfigProvider",
    "HealthCheckStatus",
    "HealthCheckResult",
    "HealthCheckFunc",
    "CommandMeta",
    "create_agent_app",
    # S03 — Backup
    "BackupMeta",
    "CreateBackup",
    "ListBackups",
    "GFSRotate",
    "RetentionPolicy",
    "RotationResult",
    "BackupConfig",
    "DefaultBackupConfig",
    "LoadBackupConfig",
    "SaveBackupConfig",
]
