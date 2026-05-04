package agentsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Test fixture types ---

type testConfig struct {
	Name     string  `json:"name" config:"true"`
	APIKey   string  `json:"api_key" sensitive:"true"`
	Port     int     `json:"port" config:"true"`
	Rate     float64 `json:"rate" config:"true"`
	Debug    bool    `json:"debug" config:"true"`
	Internal string  `json:"internal"` // no config tag → not in whitelist
}

type testConfigWithValidate struct {
	Name string `json:"name" config:"true"`
	Port int    `json:"port" config:"true"`
}

func (c testConfigWithValidate) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port must be 0-65535, got %d", c.Port)
	}
	return nil
}

type nestedConfig struct {
	Host string `json:"host" config:"true"`
	DB   struct {
		Driver string `json:"db_driver" config:"true"`
		DSN    string `json:"db_dsn" sensitive:"true"`
	} `json:"db"`
}

// --- Helper ---

func newTestConfigManager[T any](t *testing.T) (*ConfigManager[T], string) {
	t.Helper()
	dir := t.TempDir()
	fp := filepath.Join(dir, "config.json")
	return NewConfigManager[T](fp), fp
}

// --- Tests ---

func TestConfigSaveAndLoad(t *testing.T) {
	cm, fp := newTestConfigManager[testConfig](t)

	cfg := testConfig{
		Name:   "test-app",
		APIKey: "secret123",
		Port:   8080,
		Rate:   3.14,
		Debug:  true,
	}

	// Save
	if err := cm.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Load
	loaded, err := cm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Name != cfg.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, cfg.Name)
	}
	if loaded.APIKey != cfg.APIKey {
		t.Errorf("APIKey = %q, want %q", loaded.APIKey, cfg.APIKey)
	}
	if loaded.Port != cfg.Port {
		t.Errorf("Port = %d, want %d", loaded.Port, cfg.Port)
	}
	if loaded.Rate != cfg.Rate {
		t.Errorf("Rate = %f, want %f", loaded.Rate, cfg.Rate)
	}
	if loaded.Debug != cfg.Debug {
		t.Errorf("Debug = %v, want %v", loaded.Debug, cfg.Debug)
	}
}

func TestConfigSaveCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "sub", "dir", "config.json")
	cm := NewConfigManager[testConfig](fp)

	cfg := testConfig{Name: "deep"}
	if err := cm.Save(cfg); err != nil {
		t.Fatalf("Save with nested dirs: %v", err)
	}
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("file not created at nested path: %v", err)
	}
}

func TestConfigLoadMissingFile(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	_, err := cm.Load()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "config: read") {
		t.Errorf("error should wrap read error, got: %v", err)
	}
}

func TestConfigLoadMalformedJSON(t *testing.T) {
	cm, fp := newTestConfigManager[testConfig](t)

	// Write invalid JSON
	if err := os.WriteFile(fp, []byte("{bad json}"), 0644); err != nil {
		t.Fatalf("write bad file: %v", err)
	}

	_, err := cm.Load()
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "config: parse") {
		t.Errorf("error should wrap parse error, got: %v", err)
	}
}

func TestConfigRedacted(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{
		Name:   "test-app",
		APIKey: "secret123",
		Port:   8080,
	}

	redacted := cm.Redacted(cfg)

	// Sensitive field should be masked
	if redacted.APIKey != "***" {
		t.Errorf("APIKey = %q, want *** (redacted)", redacted.APIKey)
	}

	// Non-sensitive fields should be preserved
	if redacted.Name != cfg.Name {
		t.Errorf("Name = %q, want %q", redacted.Name, cfg.Name)
	}
	if redacted.Port != cfg.Port {
		t.Errorf("Port = %d, want %d", redacted.Port, cfg.Port)
	}

	// Original should not be modified
	if cfg.APIKey != "secret123" {
		t.Errorf("original APIKey modified: %q", cfg.APIKey)
	}
}

func TestConfigRedactedNested(t *testing.T) {
	cm, _ := newTestConfigManager[nestedConfig](t)

	cfg := nestedConfig{Host: "localhost"}
	cfg.DB.Driver = "postgres"
	cfg.DB.DSN = "postgres://user:pass@host/db"

	redacted := cm.Redacted(cfg)

	if redacted.DB.DSN != "***" {
		t.Errorf("nested sensitive DSN = %q, want '***'", redacted.DB.DSN)
	}
	if redacted.DB.Driver != "postgres" {
		t.Errorf("nested non-sensitive Driver = %q, want 'postgres'", redacted.DB.Driver)
	}
}

func TestConfigWhitelist(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	wl := cm.Whitelist()
	expected := map[string]bool{
		"name":  false,
		"port":  false,
		"rate":  false,
		"debug": false,
	}

	if len(wl) != len(expected) {
		t.Fatalf("Whitelist() = %v (len %d), want %d entries", wl, len(wl), len(expected))
	}

	for _, name := range wl {
		if _, ok := expected[name]; !ok {
			t.Errorf("unexpected whitelist entry: %q", name)
		}
		expected[name] = true
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing whitelist entry: %q", name)
		}
	}
}

func TestConfigWhitelistExcludesNonConfigFields(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	wl := cm.Whitelist()
	for _, name := range wl {
		if name == "api_key" {
			t.Error("api_key should not be in whitelist (not config:\"true\")")
		}
		if name == "internal" {
			t.Error("internal should not be in whitelist (no config tag)")
		}
	}
}

func TestConfigSetByPath(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{}

	// String
	if err := cm.SetByPath(&cfg, "name", "my-app"); err != nil {
		t.Fatalf("SetByPath name: %v", err)
	}
	if cfg.Name != "my-app" {
		t.Errorf("Name = %q, want %q", cfg.Name, "my-app")
	}

	// Int
	if err := cm.SetByPath(&cfg, "port", "9090"); err != nil {
		t.Fatalf("SetByPath port: %v", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9090)
	}

	// Float64
	if err := cm.SetByPath(&cfg, "rate", "2.718"); err != nil {
		t.Fatalf("SetByPath rate: %v", err)
	}
	if cfg.Rate != 2.718 {
		t.Errorf("Rate = %f, want %f", cfg.Rate, 2.718)
	}

	// Bool
	if err := cm.SetByPath(&cfg, "debug", "true"); err != nil {
		t.Fatalf("SetByPath debug: %v", err)
	}
	if cfg.Debug != true {
		t.Errorf("Debug = %v, want true", cfg.Debug)
	}
}

func TestConfigSetByPathRejectsNonConfigurable(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{}

	// api_key is sensitive:"true" but NOT config:"true"
	err := cm.SetByPath(&cfg, "api_key", "new-key")
	if err == nil {
		t.Fatal("expected error setting non-configurable field")
	}
	if !strings.Contains(err.Error(), "not configurable") {
		t.Errorf("error = %q, want 'not configurable'", err.Error())
	}
}

func TestConfigSetByPathRejectsUnknownField(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{}
	err := cm.SetByPath(&cfg, "nonexistent", "value")
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error = %q, want 'unknown field'", err.Error())
	}
}

func TestConfigSetByPathTypeConversionError(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{}

	err := cm.SetByPath(&cfg, "port", "not-a-number")
	if err == nil {
		t.Fatal("expected error for bad int conversion")
	}
	if !strings.Contains(err.Error(), "convert") {
		t.Errorf("error = %q, want 'convert'", err.Error())
	}

	err = cm.SetByPath(&cfg, "debug", "not-a-bool")
	if err == nil {
		t.Fatal("expected error for bad bool conversion")
	}
}

func TestConfigValidateWithValidator(t *testing.T) {
	cm, _ := newTestConfigManager[testConfigWithValidate](t)

	// Valid config
	valid := testConfigWithValidate{Name: "app", Port: 8080}
	if err := cm.Validate(valid); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}

	// Invalid: empty name
	invalid1 := testConfigWithValidate{Name: "", Port: 8080}
	if err := cm.Validate(invalid1); err == nil {
		t.Fatal("expected error for empty name")
	}

	// Invalid: port out of range
	invalid2 := testConfigWithValidate{Name: "app", Port: 99999}
	if err := cm.Validate(invalid2); err == nil {
		t.Fatal("expected error for port out of range")
	}
}

func TestConfigValidateWithoutValidator(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{Name: "test"}
	// testConfig does not implement Validator, so Validate should return nil
	if err := cm.Validate(cfg); err != nil {
		t.Errorf("non-validator type should return nil, got: %v", err)
	}
}

func TestConfigSaveAtomicWrite(t *testing.T) {
	cm, fp := newTestConfigManager[testConfig](t)

	cfg := testConfig{Name: "atomic-test"}
	if err := cm.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// .tmp file should NOT remain after atomic write
	tmpFile := fp + ".tmp"
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Errorf("tmp file should not exist after atomic save: %s", tmpFile)
	}

	// Verify JSON is well-formed
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("JSON should be valid: %v", err)
	}
	if result["name"] != "atomic-test" {
		t.Errorf("name = %v, want 'atomic-test'", result["name"])
	}
}

func TestConfigFilePath(t *testing.T) {
	fp := "/some/path/config.json"
	cm := NewConfigManager[testConfig](fp)
	if cm.FilePath() != fp {
		t.Errorf("FilePath() = %q, want %q", cm.FilePath(), fp)
	}
}

func TestConfigRoundTripPreservesAllFields(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	original := testConfig{
		Name:     "full-test",
		APIKey:   "key-12345",
		Port:     443,
		Rate:     1.618,
		Debug:    false,
		Internal: "should-persist",
	}

	if err := cm.Save(original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := cm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded != original {
		t.Errorf("round-trip mismatch:\ngot:  %+v\nwant: %+v", loaded, original)
	}
}

func TestConfigSetByPathThenSaveLoad(t *testing.T) {
	cm, _ := newTestConfigManager[testConfig](t)

	cfg := testConfig{}
	cm.SetByPath(&cfg, "name", "integration")
	cm.SetByPath(&cfg, "port", "3000")
	cm.SetByPath(&cfg, "debug", "true")

	if err := cm.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := cm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Name != "integration" {
		t.Errorf("Name = %q, want 'integration'", loaded.Name)
	}
	if loaded.Port != 3000 {
		t.Errorf("Port = %d, want 3000", loaded.Port)
	}
	if loaded.Debug != true {
		t.Errorf("Debug = %v, want true", loaded.Debug)
	}
}

func TestConfigWhitelistNestedFields(t *testing.T) {
	cm, _ := newTestConfigManager[nestedConfig](t)

	wl := cm.Whitelist()
	expected := map[string]bool{
		"host":      false,
		"db_driver": false,
	}

	if len(wl) != len(expected) {
		t.Fatalf("Whitelist() = %v (len %d), want %d entries", wl, len(wl), len(expected))
	}

	for _, name := range wl {
		if _, ok := expected[name]; !ok {
			t.Errorf("unexpected whitelist entry: %q", name)
		}
		expected[name] = true
	}
}
