"""CrashDump — structured crash data with atomic write.

Represents the data written to disk when the application crashes due to a
signal or panic. Uses an atomic write-to-tmp-then-rename pattern to avoid
partial files on crash.

The filename format is ``crash-YYYYMMDD-HHMMSS.json`` for easy chronological
sorting.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, Optional

from agentsdk.sandbox import Sandbox


@dataclass
class CrashDump:
    """Structured record of an application crash for post-mortem debugging."""

    timestamp: str
    app_name: str
    app_version: str
    crash_type: str
    stack_trace: str
    flight_context: Dict[str, Any] = field(default_factory=dict)
    trace_id: str = ""
    signal: str = ""
    panic_value: str = ""

    def to_dict(self) -> Dict[str, Any]:
        """Serialize to a dict, omitting empty/None fields.

        Matches Go SDK's ``omitempty`` semantics — ``trace_id``, ``signal``,
        and ``panic_value`` are excluded when empty.
        """
        d: Dict[str, Any] = {
            "timestamp": self.timestamp,
            "app_name": self.app_name,
            "app_version": self.app_version,
            "crash_type": self.crash_type,
            "stack_trace": self.stack_trace,
            "flight_context": self.flight_context,
        }
        if self.trace_id:
            d["trace_id"] = self.trace_id
        if self.signal:
            d["signal"] = self.signal
        if self.panic_value:
            d["panic_value"] = self.panic_value
        return d


def write_crash_dump(sandbox: Sandbox, dump: CrashDump) -> str:
    """Atomically write a CrashDump as formatted JSON to the crash_dumps/ directory.

    The write uses a write-to-tmp-then-rename pattern (via ``os.replace()``)
    to avoid partial files on crash.  The ``crash_dumps/`` directory is
    created automatically if it does not exist.

    Args:
        sandbox: The application Sandbox providing the crash_dumps directory.
        dump: The CrashDump to write.

    Returns:
        The path of the written crash dump file.

    Raises:
        OSError: If the directory cannot be created, the temp file cannot be
            written, or the atomic rename fails.
        TypeError: If the CrashDump cannot be serialized to JSON.
    """
    data = json.dumps(dump.to_dict(), indent=2)

    crash_dir = Path(sandbox.crash_dumps_dir)
    crash_dir.mkdir(parents=True, exist_ok=True)

    filename = f"crash-{datetime.now().strftime('%Y%m%d-%H%M%S')}.json"
    final_path = crash_dir / filename
    tmp_path = str(final_path) + ".tmp"

    with open(tmp_path, "w", encoding="utf-8") as f:
        f.write(data)

    os.replace(tmp_path, str(final_path))

    return str(final_path)
