"""Exit code constants, ExitError exception, and ErrorCodeRegistry.

Provides the shared exit code contract between the SDK and consumers.
Built-in error codes are protected from override; custom codes may be
registered and re-registered (silently updates).
"""

# ---------------------------------------------------------------------------
# Exit code constants (must match Go SDK values 0-5)
# ---------------------------------------------------------------------------

EXIT_SUCCESS = 0
EXIT_FATAL_ERROR = 1
EXIT_INVALID_PARAMS = 2
EXIT_NOT_FOUND = 3
EXIT_NETWORK_ERROR = 4
EXIT_LOCK_CONFLICT = 5

# ---------------------------------------------------------------------------
# Built-in error codes registered by every ErrorCodeRegistry instance
# ---------------------------------------------------------------------------

_BUILTIN_CODES: dict[str, tuple[int, str]] = {
    "FATAL_CRASH": (EXIT_FATAL_ERROR, "Unrecoverable process crash"),
    "INTERNAL_ERROR": (EXIT_FATAL_ERROR, "Internal SDK error"),
    "INPUT_INVALID": (EXIT_INVALID_PARAMS, "Invalid input parameter"),
    "NOT_FOUND": (EXIT_NOT_FOUND, "Requested resource not found"),
    "RESOURCE_LOCKED": (EXIT_LOCK_CONFLICT, "Resource is locked by another process"),
}


class ExitError(Exception):
    """Exception carrying a semantic exit code and an optional wrapped error."""

    def __init__(self, code: int, message: str, original_error: Exception | None = None):
        self.code = code
        self.message = message
        self.original_error = original_error
        super().__init__(str(self))

    def __str__(self) -> str:
        return f"exit {self.code}: {self.message}"


class ErrorCodeRegistry:
    """Registry mapping string error codes to (exit_code, description) pairs.

    Five built-in codes are always present and **cannot** be overridden.
    Custom codes may be registered or re-registered (update semantics).
    """

    def __init__(self) -> None:
        # internal: code -> (exit_code, description)
        self._codes: dict[str, tuple[int, str]] = dict(_BUILTIN_CODES)

    # -- mutators -----------------------------------------------------------

    def register(self, code: str, exit_code: int, description: str) -> None:
        """Register a custom error code.  Raises ValueError for built-in codes."""
        if code in _BUILTIN_CODES:
            raise ValueError(f"Cannot override built-in error code: {code}")
        self._codes[code] = (exit_code, description)

    # -- accessors ----------------------------------------------------------

    def lookup(self, code: str) -> tuple[int, str, bool]:
        """Look up *code*.

        Returns ``(exit_code, description, found)``.
        Unknown codes yield ``(EXIT_FATAL_ERROR, "", False)``.
        """
        if code in self._codes:
            exit_code, description = self._codes[code]
            return (exit_code, description, True)
        return (EXIT_FATAL_ERROR, "", False)

    def to_exit_code(self, code: str) -> int:
        """Return the numeric exit code for *code*, or EXIT_FATAL_ERROR."""
        exit_code, _, found = self.lookup(code)
        if not found:
            return EXIT_FATAL_ERROR
        return exit_code

    def has_error_code(self, code: str) -> bool:
        """Return ``True`` if *code* is registered (built-in or custom)."""
        return self.lookup(code)[2]

    def all_codes(self) -> dict[str, tuple[int, str]]:
        """Return a **defensive copy** of all registered codes."""
        return dict(self._codes)
