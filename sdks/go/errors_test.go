package agentsdk

import (
	"errors"
	"testing"
)

// --- Structured error type tests ---

func TestWhitelistErrorImplementsInterface(t *testing.T) {
	err := newWhitelistError("secret_field")

	// errors.As must find WhitelistError.
	var wlErr WhitelistError
	if !errors.As(err, &wlErr) {
		t.Fatal("errors.As failed to match WhitelistError")
	}
	if wlErr.Field() != "secret_field" {
		t.Errorf("Field() = %q, want %q", wlErr.Field(), "secret_field")
	}
	if !wlErr.IsWhitelistError() {
		t.Error("IsWhitelistError() = false, want true")
	}
}

func TestUnknownFieldErrorImplementsInterface(t *testing.T) {
	err := newUnknownFieldError("nonexistent")

	var ufErr UnknownFieldError
	if !errors.As(err, &ufErr) {
		t.Fatal("errors.As failed to match UnknownFieldError")
	}
	if ufErr.Field() != "nonexistent" {
		t.Errorf("Field() = %q, want %q", ufErr.Field(), "nonexistent")
	}
	if !ufErr.IsUnknownFieldError() {
		t.Error("IsUnknownFieldError() = false, want true")
	}
}

func TestWhitelistErrorDoesNotMatchUnknown(t *testing.T) {
	err := newWhitelistError("x")
	var ufErr UnknownFieldError
	if errors.As(err, &ufErr) {
		t.Error("WhitelistError should not match UnknownFieldError interface")
	}
}

func TestUnknownFieldErrorDoesNotMatchWhitelist(t *testing.T) {
	err := newUnknownFieldError("x")
	var wlErr WhitelistError
	if errors.As(err, &wlErr) {
		t.Error("UnknownFieldError should not match WhitelistError interface")
	}
}

func TestWhitelistErrorMessage(t *testing.T) {
	err := newWhitelistError("token")
	want := "config: field token is not configurable (not in whitelist)"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestUnknownFieldErrorMessage(t *testing.T) {
	err := newUnknownFieldError("bogus")
	want := "config: unknown field bogus"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

// --- ConfigManager.SetByPath structured error tests ---

func TestSetByPathReturnsUnknownFieldError(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)
	var cfg testConfig

	err := cm.SetByPath(&cfg, "nonexistent_field", "value")
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}

	var ufErr UnknownFieldError
	if !errors.As(err, &ufErr) {
		t.Fatalf("expected UnknownFieldError, got %T: %v", err, err)
	}
	if ufErr.Field() != "nonexistent_field" {
		t.Errorf("Field() = %q, want %q", ufErr.Field(), "nonexistent_field")
	}
}

func TestSetByPathReturnsWhitelistError(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)
	var cfg testConfig

	// "internal" has no config:"true" tag → whitelist error
	err := cm.SetByPath(&cfg, "internal", "value")
	if err == nil {
		t.Fatal("expected error for non-whitelisted field, got nil")
	}

	var wlErr WhitelistError
	if !errors.As(err, &wlErr) {
		t.Fatalf("expected WhitelistError, got %T: %v", err, err)
	}
	if wlErr.Field() != "internal" {
		t.Errorf("Field() = %q, want %q", wlErr.Field(), "internal")
	}
}

func TestSetByPathSuccessNoError(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)
	var cfg testConfig

	err := cm.SetByPath(&cfg, "name", "alice")
	if err != nil {
		t.Fatalf("expected no error for whitelisted field, got %v", err)
	}
	if cfg.Name != "alice" {
		t.Errorf("Name = %q, want %q", cfg.Name, "alice")
	}
}

// --- App.Registry() test ---

func TestAppRegistryGetter(t *testing.T) {
	app := New("test-app", "1.0.0")
	reg := app.Registry()
	if reg == nil {
		t.Fatal("Registry() returned nil")
	}

	// Verify it's the same registry used by RegisterErrorCode.
	err := app.RegisterErrorCode("TEST_CUSTOM", 42, "test code")
	if err != nil {
		t.Fatalf("RegisterErrorCode failed: %v", err)
	}

	exitCode, desc, ok := reg.Lookup("TEST_CUSTOM")
	if !ok {
		t.Fatal("Lookup(TEST_CUSTOM) not found")
	}
	if exitCode != 42 {
		t.Errorf("exitCode = %d, want 42", exitCode)
	}
	if desc != "test code" {
		t.Errorf("desc = %q, want %q", desc, "test code")
	}
}

func TestAppRegistryReturnsBuiltinCodes(t *testing.T) {
	app := New("test-app", "1.0.0")
	reg := app.Registry()

	exitCode, _, ok := reg.Lookup("FATAL_CRASH")
	if !ok {
		t.Fatal("built-in FATAL_CRASH not found via Registry()")
	}
	if exitCode != ExitFatalError {
		t.Errorf("FATAL_CRASH exitCode = %d, want %d", exitCode, ExitFatalError)
	}
}
