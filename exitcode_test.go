package agentsdk

import (
	"errors"
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// Exit code constants
// ---------------------------------------------------------------------------

func TestExitCodeValues(t *testing.T) {
	tests := []struct {
		name     string
		got      int
		expected int
	}{
		{"ExitSuccess", ExitSuccess, 0},
		{"ExitFatalError", ExitFatalError, 1},
		{"ExitInvalidParams", ExitInvalidParams, 2},
		{"ExitNotFound", ExitNotFound, 3},
		{"ExitNetworkError", ExitNetworkError, 4},
		{"ExitLockConflict", ExitLockConflict, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %d, want %d", tt.name, tt.got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ExitError
// ---------------------------------------------------------------------------

func TestExitError_Error(t *testing.T) {
	inner := errors.New("something broke")
	ee := &ExitError{Code: ExitFatalError, Err: inner}

	want := "exit 1: something broke"
	if got := ee.Error(); got != want {
		t.Errorf("ExitError.Error() = %q, want %q", got, want)
	}
}

func TestExitError_Unwrap(t *testing.T) {
	inner := errors.New("root cause")
	ee := &ExitError{Code: ExitInvalidParams, Err: inner}

	if unwrapped := ee.Unwrap(); unwrapped != inner {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, inner)
	}
}

func TestExitError_errorsIs(t *testing.T) {
	inner := errors.New("base")
	ee := &ExitError{Code: ExitNotFound, Err: inner}

	if !errors.Is(ee, inner) {
		t.Error("errors.Is(ExitError, inner) should be true")
	}
}

func TestExitError_errorsAs(t *testing.T) {
	inner := errors.New("base")
	ee := &ExitError{Code: ExitLockConflict, Err: inner}

	var target *ExitError
	if !errors.As(ee, &target) {
		t.Error("errors.As should match *ExitError")
	}
	if target.Code != ExitLockConflict {
		t.Errorf("as target code = %d, want %d", target.Code, ExitLockConflict)
	}
}

// ---------------------------------------------------------------------------
// ErrorCodeRegistry — built-in codes
// ---------------------------------------------------------------------------

func TestBuiltInErrorCodes(t *testing.T) {
	r := NewErrorCodeRegistry()

	tests := []struct {
		code        string
		wantExit    int
		wantDesc    string
	}{
		{"FATAL_CRASH", ExitFatalError, "程序崩溃（panic 恢复）"},
		{"INTERNAL_ERROR", ExitFatalError, "内部错误"},
		{"INPUT_INVALID", ExitInvalidParams, "输入参数无效"},
		{"NOT_FOUND", ExitNotFound, "资源未找到"},
		{"RESOURCE_LOCKED", ExitLockConflict, "资源互斥锁定"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			exitCode, desc, ok := r.Lookup(tt.code)
			if !ok {
				t.Fatalf("Lookup(%q) not found", tt.code)
			}
			if exitCode != tt.wantExit {
				t.Errorf("Lookup(%q) exitCode = %d, want %d", tt.code, exitCode, tt.wantExit)
			}
			if desc != tt.wantDesc {
				t.Errorf("Lookup(%q) desc = %q, want %q", tt.code, desc, tt.wantDesc)
			}
		})
	}
}

func TestLookupNotFound(t *testing.T) {
	r := NewErrorCodeRegistry()
	_, _, ok := r.Lookup("NONEXISTENT")
	if ok {
		t.Error("Lookup(NONEXISTENT) should return ok=false")
	}
}

// ---------------------------------------------------------------------------
// ErrorCodeRegistry — Register (custom codes)
// ---------------------------------------------------------------------------

func TestRegisterCustomCode(t *testing.T) {
	r := NewErrorCodeRegistry()

	err := r.Register("CUSTOM_ERR", 10, "自定义错误")
	if err != nil {
		t.Fatalf("Register() returned unexpected error: %v", err)
	}

	exitCode, desc, ok := r.Lookup("CUSTOM_ERR")
	if !ok {
		t.Fatal("Lookup(CUSTOM_ERR) not found after Register")
	}
	if exitCode != 10 {
		t.Errorf("CUSTOM_ERR exitCode = %d, want 10", exitCode)
	}
	if desc != "自定义错误" {
		t.Errorf("CUSTOM_ERR desc = %q, want %q", desc, "自定义错误")
	}
}

func TestRegisterRejectsBuiltInOverride(t *testing.T) {
	r := NewErrorCodeRegistry()

	builtInCodes := []string{"FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"}
	for _, code := range builtInCodes {
		t.Run(code, func(t *testing.T) {
			err := r.Register(code, 99, "override attempt")
			if err == nil {
				t.Errorf("Register(%q) should reject built-in override", code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ErrorCodeRegistry — ToExitCode
// ---------------------------------------------------------------------------

func TestToExitCode_KnownCodes(t *testing.T) {
	r := NewErrorCodeRegistry()

	tests := []struct {
		code     string
		expected int
	}{
		{"FATAL_CRASH", ExitFatalError},
		{"INPUT_INVALID", ExitInvalidParams},
		{"NOT_FOUND", ExitNotFound},
		{"RESOURCE_LOCKED", ExitLockConflict},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := r.ToExitCode(tt.code)
			if got != tt.expected {
				t.Errorf("ToExitCode(%q) = %d, want %d", tt.code, got, tt.expected)
			}
		})
	}
}

func TestToExitCode_FallbackForUnknown(t *testing.T) {
	r := NewErrorCodeRegistry()

	got := r.ToExitCode("COMPLETELY_UNKNOWN")
	if got != ExitFatalError {
		t.Errorf("ToExitCode(unknown) = %d, want fallback %d", got, ExitFatalError)
	}
}

func TestToExitCode_CustomCode(t *testing.T) {
	r := NewErrorCodeRegistry()
	_ = r.Register("CUSTOM_NET", ExitNetworkError, "自定义网络错误")

	got := r.ToExitCode("CUSTOM_NET")
	if got != ExitNetworkError {
		t.Errorf("ToExitCode(CUSTOM_NET) = %d, want %d", got, ExitNetworkError)
	}
}

// ---------------------------------------------------------------------------
// ErrorCodeRegistry — AllCodes
// ---------------------------------------------------------------------------

func TestAllCodes_IncludesBuiltInAndCustom(t *testing.T) {
	r := NewErrorCodeRegistry()
	_ = r.Register("MY_CUSTOM", 42, "自定义")

	all := r.AllCodes()

	// 5 built-in + 1 custom = 6
	if len(all) != 6 {
		t.Errorf("AllCodes() has %d entries, want 6", len(all))
	}

	// Verify a built-in
	if entry, ok := all["FATAL_CRASH"]; !ok || entry.ExitCode != ExitFatalError {
		t.Error("AllCodes() missing FATAL_CRASH or wrong exit code")
	}

	// Verify the custom code
	if entry, ok := all["MY_CUSTOM"]; !ok || entry.ExitCode != 42 || entry.Description != "自定义" {
		t.Error("AllCodes() missing MY_CUSTOM or wrong values")
	}
}

func TestAllCodes_IsCopy(t *testing.T) {
	r := NewErrorCodeRegistry()

	all := r.AllCodes()
	all["INJECTED"] = errorCodeEntry{ExitCode: 99, Description: "should not leak"}

	_, _, ok := r.Lookup("INJECTED")
	if ok {
		t.Error("modifying AllCodes() result should not affect the registry")
	}
}

func TestAllCodes_NewRegistryHasBuiltInsOnly(t *testing.T) {
	r := NewErrorCodeRegistry()
	all := r.AllCodes()

	if len(all) != len(builtInErrorCodes) {
		t.Errorf("new registry AllCodes() has %d entries, want %d", len(all), len(builtInErrorCodes))
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestRegisterSameCustomCodeTwice(t *testing.T) {
	r := NewErrorCodeRegistry()

	err := r.Register("MY_CODE", 10, "first")
	if err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	// Re-registering a custom code should overwrite (not an error — it's not built-in).
	err = r.Register("MY_CODE", 20, "second")
	if err != nil {
		t.Fatalf("second Register failed: %v", err)
	}

	exitCode, desc, _ := r.Lookup("MY_CODE")
	if exitCode != 20 || desc != "second" {
		t.Errorf("after re-register: code=%d desc=%q, want code=20 desc=%q", exitCode, desc, "second")
	}
}

func TestExitError_NilErr(t *testing.T) {
	ee := &ExitError{Code: ExitSuccess, Err: nil}
	// Verify it doesn't panic and produces a string.
	got := ee.Error()
	if got == "" {
		t.Error("ExitError.Error() with nil Err should return non-empty string")
	}
}

func TestExitError_ImplementsError(t *testing.T) {
	// Compile-time check: ExitError must implement error.
	var _ error = (*ExitError)(nil)
}

// Ensure fmt.Errorf wrapping works with ExitError for %w chaining.
func TestExitError_WrappedInFmtErrorf(t *testing.T) {
	inner := errors.New("base")
	ee := &ExitError{Code: ExitInvalidParams, Err: inner}
	wrapped := fmt.Errorf("wrapper: %w", ee)

	var target *ExitError
	if !errors.As(wrapped, &target) {
		t.Error("errors.As should unwrap to *ExitError through fmt.Errorf")
	}
	if target.Code != ExitInvalidParams {
		t.Errorf("unwrapped code = %d, want %d", target.Code, ExitInvalidParams)
	}
}
