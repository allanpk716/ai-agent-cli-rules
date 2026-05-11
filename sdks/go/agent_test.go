package agentsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestAgentCommandsNoExtraBackwardCompat verifies that AgentCommands() with
// no extra args returns exactly 6 sub-commands (backward compatibility).
func TestAgentCommandsNoExtraBackwardCompat(t *testing.T) {
	app := New("compat-tool", "1.0.0")
	cmd := app.AgentCommands()

	subs := cmd.Commands()
	if len(subs) != 6 {
		t.Fatalf("expected 6 sub-commands with no extra args, got %d", len(subs))
	}

	expected := map[string]bool{
		"schema": false, "errors": false, "config": false,
		"doctor": false, "debug": false, "cache": false,
	}
	for _, sub := range subs {
		if _, ok := expected[sub.Name()]; !ok {
			t.Errorf("unexpected sub-command %q", sub.Name())
		}
		expected[sub.Name()] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing sub-command %q", name)
		}
	}
}

// TestAgentCommandsExtraSubcommands verifies that extra cobra.Command arguments
// are appended to the agent tree as additional sub-commands.
func TestAgentCommandsExtraSubcommands(t *testing.T) {
	app := New("extra-tool", "1.0.0")

	customCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the daemon process",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	cmd := app.AgentCommands(customCmd)

	subs := cmd.Commands()
	if len(subs) != 7 {
		t.Fatalf("expected 7 sub-commands (6 standard + 1 extra), got %d", len(subs))
	}

	// Verify custom command is present.
	found := false
	for _, sub := range subs {
		if sub.Name() == "daemon" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'daemon' in sub-commands")
	}
}

// TestAgentCommandsExtraAppearsInSchema verifies that extra commands passed to
// AgentCommands appear in the agent schema output.
func TestAgentCommandsExtraAppearsInSchema(t *testing.T) {
	app, buf := newTestApp("schema-extra-tool", "1.0.0")

	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the daemon process",
		Run:   func(cmd *cobra.Command, args []string) {},
	}

	rootCmd := &cobra.Command{Use: "schema-extra-tool"}
	rootCmd.AddCommand(app.AgentCommands(daemonCmd))
	rootCmd.SetArgs([]string{"agent", "schema"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	dataBytes, _ := json.Marshal(env.Data)
	var schema schemaOutput
	json.Unmarshal(dataBytes, &schema)

	foundDaemon := false
	for _, cmd := range schema.Commands {
		if cmd.Name == "agent daemon" {
			foundDaemon = true
			if cmd.Description != "Run the daemon process" {
				t.Errorf("daemon Description = %q, want %q", cmd.Description, "Run the daemon process")
			}
		}
	}
	if !foundDaemon {
		t.Error("expected 'agent daemon' in schema commands")
	}
}

// TestAgentSchemaOutputsMetadata verifies that agent schema emits valid JSONL
// with tool name, version, and command entries from the Cobra tree.
func TestAgentSchemaOutputsMetadata(t *testing.T) {
	app, buf := newTestApp("test-tool", "2.0.0")

	// Build a command tree.
	rootCmd := &cobra.Command{Use: "test-tool"}
	subCmd1 := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the application",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	subCmd2 := &cobra.Command{
		Use:   "status",
		Short: "Check status",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	rootCmd.AddCommand(subCmd1)
	rootCmd.AddCommand(subCmd2)

	// Add agent commands.
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "schema"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	output := strings.TrimSpace(buf.String())
	env, err := parseEnvelope(output)
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	// Validate envelope structure.
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}

	// Parse data payload.
	dataBytes, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}

	var schema schemaOutput
	if err := json.Unmarshal(dataBytes, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	if schema.Tool != "test-tool" {
		t.Errorf("Tool = %q, want %q", schema.Tool, "test-tool")
	}
	if schema.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", schema.Version, "2.0.0")
	}
	if len(schema.Commands) == 0 {
		t.Fatal("expected non-empty commands list")
	}

	// Verify that "deploy" and "status" are in the schema.
	foundDeploy := false
	foundStatus := false
	for _, cmd := range schema.Commands {
		if cmd.Name == "deploy" {
			foundDeploy = true
		}
		if cmd.Name == "status" {
			foundStatus = true
		}
	}
	if !foundDeploy {
		t.Error("expected 'deploy' in schema commands")
	}
	if !foundStatus {
		t.Error("expected 'status' in schema commands")
	}
}

// TestAgentSchemaWithCommandMeta verifies that RegisterCommandMeta enriches
// the schema output with description and idempotency metadata.
func TestAgentSchemaWithCommandMeta(t *testing.T) {
	app, buf := newTestApp("meta-tool", "1.0.0")

	// Register command metadata.
	app.RegisterCommandMeta("deploy", CommandMeta{
		Description:  "Deploy the application to production",
		IsIdempotent: true,
	})
	app.RegisterCommandMeta("status", CommandMeta{
		Description: "Get the current deployment status",
	})

	// Build a command tree.
	rootCmd := &cobra.Command{Use: "meta-tool"}
	rootCmd.AddCommand(&cobra.Command{Use: "deploy", Short: "old desc", Run: func(cmd *cobra.Command, args []string) {}})
	rootCmd.AddCommand(&cobra.Command{Use: "status", Short: "old status desc", Run: func(cmd *cobra.Command, args []string) {}})
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "schema"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	dataBytes, _ := json.Marshal(env.Data)
	var schema schemaOutput
	json.Unmarshal(dataBytes, &schema)

	// Find "deploy" and verify meta was merged.
	var deployEntry *commandEntry
	for i := range schema.Commands {
		if schema.Commands[i].Name == "deploy" {
			deployEntry = &schema.Commands[i]
			break
		}
	}
	if deployEntry == nil {
		t.Fatal("expected 'deploy' in schema commands")
	}
	if deployEntry.Description != "Deploy the application to production" {
		t.Errorf("deploy Description = %q, want enriched description", deployEntry.Description)
	}
	if !deployEntry.IsIdempotent {
		t.Error("deploy IsIdempotent = false, want true")
	}
}

// TestAgentErrorsOutputsCodes verifies that agent errors emits all registered
// error codes (built-in + custom) as valid JSONL.
func TestAgentErrorsOutputsCodes(t *testing.T) {
	app, buf := newTestApp("err-tool", "1.0.0")

	// Register a custom error code.
	app.RegisterErrorCode("CUSTOM_ERR", 42, "custom error for testing")
	app.RegisterErrorCode("ANOTHER_ERR", 99, "another custom error")

	rootCmd := &cobra.Command{Use: "err-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "errors"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}

	dataBytes, _ := json.Marshal(env.Data)
	var errorsOut errorsOutput
	json.Unmarshal(dataBytes, &errorsOut)

	if errorsOut.Count == 0 {
		t.Fatal("expected non-zero error codes count")
	}

	// Check that built-in codes are present.
	codeMap := make(map[string]errorEntry)
	for _, e := range errorsOut.Codes {
		codeMap[e.Code] = e
	}

	builtins := []string{"FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"}
	for _, b := range builtins {
		if _, ok := codeMap[b]; !ok {
			t.Errorf("built-in code %q not found in output", b)
		}
	}

	// Check custom codes.
	if entry, ok := codeMap["CUSTOM_ERR"]; !ok {
		t.Error("CUSTOM_ERR not found in output")
	} else if entry.ExitCode != 42 {
		t.Errorf("CUSTOM_ERR ExitCode = %d, want 42", entry.ExitCode)
	}

	if entry, ok := codeMap["ANOTHER_ERR"]; !ok {
		t.Error("ANOTHER_ERR not found in output")
	} else if entry.ExitCode != 99 {
		t.Errorf("ANOTHER_ERR ExitCode = %d, want 99", entry.ExitCode)
	}

	// Verify codes are sorted.
	for i := 1; i < len(errorsOut.Codes); i++ {
		if errorsOut.Codes[i-1].Code > errorsOut.Codes[i].Code {
			t.Errorf("codes not sorted: %q > %q at index %d", errorsOut.Codes[i-1].Code, errorsOut.Codes[i].Code, i)
		}
	}
}

// TestAgentCommandsReturnsSix verifies that AgentCommands() returns a parent
// command with exactly 6 sub-commands.
func TestAgentCommandsReturnsSix(t *testing.T) {
	app := New("count-tool", "1.0.0")
	cmd := app.AgentCommands()

	if cmd.Use != "agent" {
		t.Errorf("Use = %q, want %q", cmd.Use, "agent")
	}

	subs := cmd.Commands()
	if len(subs) != 6 {
		t.Fatalf("expected 6 sub-commands, got %d", len(subs))
	}

	// Verify the 6 expected sub-command names.
	expected := map[string]bool{
		"schema": false,
		"errors": false,
		"config": false,
		"doctor": false,
		"debug":  false,
		"cache":  false,
	}

	for _, sub := range subs {
		if _, ok := expected[sub.Name()]; !ok {
			t.Errorf("unexpected sub-command %q", sub.Name())
		}
		expected[sub.Name()] = true
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing sub-command %q", name)
		}
	}
}

// TestAgentSchemaWithFlags verifies that flags are extracted into the schema.
func TestAgentSchemaWithFlags(t *testing.T) {
	app, buf := newTestApp("flag-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "flag-tool"}
	deployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the application",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	deployCmd.Flags().String("env", "dev", "Target environment")
	deployCmd.Flags().Bool("force", false, "Force deployment")
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "schema"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, _ := parseEnvelope(strings.TrimSpace(buf.String()))
	dataBytes, _ := json.Marshal(env.Data)
	var schema schemaOutput
	json.Unmarshal(dataBytes, &schema)

	// Find deploy entry.
	var deploy *commandEntry
	for i := range schema.Commands {
		if schema.Commands[i].Name == "deploy" {
			deploy = &schema.Commands[i]
			break
		}
	}
	if deploy == nil {
		t.Fatal("deploy command not found in schema")
	}

	flagMap := make(map[string]flagEntry)
	for _, f := range deploy.Flags {
		flagMap[f.Name] = f
	}

	if _, ok := flagMap["env"]; !ok {
		t.Error("expected 'env' flag in deploy schema")
	}
	if _, ok := flagMap["force"]; !ok {
		t.Error("expected 'force' flag in deploy schema")
	}
}

// TestAgentSchemaNestedCommands verifies that nested command paths are flattened.
func TestAgentSchemaNestedCommands(t *testing.T) {
	app, buf := newTestApp("nested-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "nested-tool"}
	parentCmd := &cobra.Command{Use: "db", Short: "Database operations"}
	childCmd := &cobra.Command{Use: "migrate", Short: "Run migrations", Run: func(cmd *cobra.Command, args []string) {}}
	parentCmd.AddCommand(childCmd)
	rootCmd.AddCommand(parentCmd)
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "schema"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, _ := parseEnvelope(strings.TrimSpace(buf.String()))
	dataBytes, _ := json.Marshal(env.Data)
	var schema schemaOutput
	json.Unmarshal(dataBytes, &schema)

	foundNested := false
	for _, cmd := range schema.Commands {
		if cmd.Name == "db migrate" {
			foundNested = true
			if cmd.Description != "Run migrations" {
				t.Errorf("nested Description = %q, want %q", cmd.Description, "Run migrations")
			}
		}
	}
	if !foundNested {
		t.Error("expected 'db migrate' in schema commands")
	}
}

// TestAgentDebugLastCrashNoDumps verifies debug last-crash when no dumps exist.
func TestAgentDebugLastCrashNoDumps(t *testing.T) {
	app, buf := newTestApp("debug-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "debug-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})

	code := app.Execute(rootCmd)

	if code != ExitNotFound {
		t.Errorf("Execute() = %d, want %d", code, ExitNotFound)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "NOT_FOUND" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "NOT_FOUND")
	}
}

// TestAgentCacheCleanEmpty verifies cache clean on an empty cache dir emits valid JSONL.
func TestAgentCacheCleanEmpty(t *testing.T) {
	app, buf := newTestApp("cache-tool", "1.0.0")

	// Ensure sandbox directories exist.
	app.sandbox.Ensure()

	rootCmd := &cobra.Command{Use: "cache-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "cache", "clean"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}

	// Verify data payload structure.
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatal("Data is not a map")
	}
	if cleaned, ok := dataMap["cleaned"].(float64); !ok || int(cleaned) != 0 {
		t.Errorf("cleaned = %v, want 0", dataMap["cleaned"])
	}
	if cacheDir, ok := dataMap["cache_dir"].(string); !ok || cacheDir == "" {
		t.Error("cache_dir is empty or missing")
	}
}

// TestAgentDoctor verifies agent doctor emits sandbox health results.
func TestAgentDoctor(t *testing.T) {
	app, buf := newTestApp("doctor-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "doctor-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "doctor"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}
}

// TestAgentConfigNoSubcommand verifies config without a sub-command shows help (exit 0).
func TestAgentConfigNoSubcommand(t *testing.T) {
	app, _ := newTestApp("config-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "config-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config"})

	code := app.Execute(rootCmd)

	// Cobra shows help for parent command without subcommand — exit success.
	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}
}

// TestConfigProviderInterface verifies that ConfigManager[T] satisfies ConfigProvider.
func TestConfigProviderInterface(t *testing.T) {
	app, _ := newTestApp("provider-tool", "1.0.0")

	// This should compile — proves the interface is satisfied.
	var _ ConfigProvider = (*ConfigManager[testConfig])(nil)

	// Register should work without panic.
	app.RegisterConfig("test", NewConfigManager[testConfig]("nonexistent.json"))
}

// TestRegisterCommandMeta verifies the registration method works.
func TestRegisterCommandMeta(t *testing.T) {
	app := New("meta-reg-tool", "1.0.0")

	app.RegisterCommandMeta("deploy", CommandMeta{
		Description:  "Deploy to cloud",
		IsIdempotent: true,
	})

	if meta, ok := app.commandMeta["deploy"]; !ok {
		t.Error("commandMeta['deploy'] not found")
	} else if meta.Description != "Deploy to cloud" {
		t.Errorf("Description = %q, want %q", meta.Description, "Deploy to cloud")
	} else if !meta.IsIdempotent {
		t.Error("IsIdempotent = false, want true")
	}
}

// TestRegisterHealthCheck verifies the registration method works.
func TestRegisterHealthCheck(t *testing.T) {
	app := New("hc-reg-tool", "1.0.0")

	called := false
	app.RegisterHealthCheck("db", func() HealthCheckResult {
		called = true
		return HealthCheckResult{
			Name:    "db",
			Status:  HealthCheckPass,
			Message: "ok",
		}
	})

	if fn, ok := app.healthChecks["db"]; !ok {
		t.Error("healthChecks['db'] not found")
	} else {
		result := fn()
		if result.Status != HealthCheckPass {
			t.Errorf("Status = %q, want %q", result.Status, HealthCheckPass)
		}
		if !called {
			t.Error("health check function was not called")
		}
	}
}

// TestWalkCommandsWithFlags verifies that walkCommands extracts flags properly
// without using the full Execute path.
func TestWalkCommandsWithFlags(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	sub := &cobra.Command{Use: "build", Short: "Build the project", Run: func(cmd *cobra.Command, args []string) {}}
	sub.Flags().String("output", "dist", "Output directory")
	sub.Flags().Bool("verbose", false, "Verbose output")
	root.AddCommand(sub)

	entries := walkCommands(root, "")

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Name != "build" {
		t.Errorf("Name = %q, want %q", entries[0].Name, "build")
	}
	if len(entries[0].Flags) != 2 {
		t.Fatalf("expected 2 flags, got %d", len(entries[0].Flags))
	}

	flagNames := make(map[string]bool)
	for _, f := range entries[0].Flags {
		flagNames[f.Name] = true
	}
	if !flagNames["output"] {
		t.Error("expected 'output' flag")
	}
	if !flagNames["verbose"] {
		t.Error("expected 'verbose' flag")
	}
}

// TestAgentSchemaIncludesAgentCommands verifies that agent commands are included
// in the schema output (per spec: "所有命令").
func TestAgentSchemaIncludesAgentCommands(t *testing.T) {
	app, buf := newTestApp("inc-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "inc-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "schema"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, _ := parseEnvelope(strings.TrimSpace(buf.String()))
	dataBytes, _ := json.Marshal(env.Data)
	var schema schemaOutput
	json.Unmarshal(dataBytes, &schema)

	// Check that agent sub-commands are included.
	foundAgentSchema := false
	foundAgentErrors := false
	for _, cmd := range schema.Commands {
		if strings.HasPrefix(cmd.Name, "agent") {
			if cmd.Name == "agent schema" {
				foundAgentSchema = true
			}
			if cmd.Name == "agent errors" {
				foundAgentErrors = true
			}
		}
	}

	if !foundAgentSchema {
		t.Error("expected 'agent schema' in commands (spec: 所有命令)")
	}
	if !foundAgentErrors {
		t.Error("expected 'agent errors' in commands (spec: 所有命令)")
	}
}

// TestAgentSchemaWithNestedFlags verifies that deeply nested commands with flags
// appear with correct path.
func TestAgentSchemaWithNestedFlags(t *testing.T) {
	app, buf := newTestApp("nest-flag-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "nest-flag-tool"}
	dbCmd := &cobra.Command{Use: "db", Short: "Database ops"}
	migrateCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run migrations",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	migrateCmd.Flags().String("direction", "up", "Migration direction")
	dbCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(dbCmd)
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "schema"})

	app.Execute(rootCmd)

	env, _ := parseEnvelope(strings.TrimSpace(buf.String()))
	dataBytes, _ := json.Marshal(env.Data)
	var schema schemaOutput
	json.Unmarshal(dataBytes, &schema)

	var migrateEntry *commandEntry
	for i := range schema.Commands {
		if schema.Commands[i].Name == "db migrate" {
			migrateEntry = &schema.Commands[i]
			break
		}
	}
	if migrateEntry == nil {
		t.Fatal("expected 'db migrate' in schema")
	}
	if len(migrateEntry.Flags) == 0 {
		t.Fatal("expected 'db migrate' to have flags")
	}

	foundDir := false
	for _, f := range migrateEntry.Flags {
		if f.Name == "direction" {
			foundDir = true
		}
	}
	if !foundDir {
		t.Error("expected 'direction' flag in db migrate")
	}
}

// TestAllEnvelopesPassValidation runs every agent command and checks all
// output envelopes pass ValidateEnvelope.
func TestAllEnvelopesPassValidation(t *testing.T) {
	app := New("val-tool", "1.0.0")
	app.sandbox.Ensure()

	commands := []struct {
		name string
		args []string
	}{
		{"schema", []string{"agent", "schema"}},
		{"errors", []string{"agent", "errors"}},
		{"doctor", []string{"agent", "doctor"}},
		{"cache clean", []string{"agent", "cache", "clean"}},
	}

	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			app.SetWriter(NewWriter(buf, "val-tool"))

			rootCmd := &cobra.Command{Use: "val-tool"}
			rootCmd.AddCommand(app.AgentCommands())
			rootCmd.SetArgs(tc.args)

			app.Execute(rootCmd)

			envs, err := parseEnvelopes(buf.String())
			if err != nil {
				t.Fatalf("parse envelopes: %v", err)
			}

			for i, env := range envs {
				if err := ValidateEnvelope(env); err != nil {
					t.Errorf("command %q envelope[%d] failed validation: %v", tc.name, i, err)
				}
			}
		})
	}
}

// --- T02: Agent config list/set and doctor tests ---

// TestAgentConfigListRedacted verifies that config list emits redacted config
// with sensitive fields masked as ***.
func TestAgentConfigListRedacted(t *testing.T) {
	app, buf := newTestApp("clist-tool", "1.0.0")

	// Create a config file with sensitive fields.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgData := `{"name":"test-app","api_key":"secret-key-123","port":8080,"rate":1.5,"debug":false,"internal":"hidden"}`
	if err := os.WriteFile(configPath, []byte(cfgData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm := NewConfigManager[testConfig](configPath)
	app.RegisterConfig("test", cm)

	rootCmd := &cobra.Command{Use: "clist-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config", "list"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Fatalf("Type = %q, want %q", env.Type, TypeResult)
	}

	// Extract the config from the data payload.
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatal("Data is not a map")
	}
	configVal, ok := dataMap["config"]
	if !ok {
		t.Fatal("Data missing 'config' key")
	}
	configMap, ok := configVal.(map[string]interface{})
	if !ok {
		t.Fatal("config value is not a map")
	}

	// Verify sensitive field is redacted.
	if configMap["api_key"] != "***" {
		t.Errorf("api_key = %q, want %q (redacted)", configMap["api_key"], "***")
	}
	// Verify non-sensitive field is present unmasked.
	if configMap["name"] != "test-app" {
		t.Errorf("name = %q, want %q", configMap["name"], "test-app")
	}
	if configMap["port"] != float64(8080) {
		t.Errorf("port = %v, want 8080", configMap["port"])
	}
}

// TestAgentConfigListNoProvider verifies that config list emits INPUT_INVALID
// when no config provider is registered.
func TestAgentConfigListNoProvider(t *testing.T) {
	app, buf := newTestApp("cnoprovider-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "cnoprovider-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config", "list"})

	code := app.Execute(rootCmd)
	if code != ExitInvalidParams {
		t.Fatalf("Execute() = %d, want %d", code, ExitInvalidParams)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "INPUT_INVALID" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "INPUT_INVALID")
	}
}

// TestAgentConfigSetSuccess verifies that config set works on a whitelisted field.
func TestAgentConfigSetSuccess(t *testing.T) {
	app, buf := newTestApp("cset-tool", "1.0.0")

	// Create initial config file.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgData := `{"name":"original","api_key":"secret","port":3000,"rate":1.0,"debug":false,"internal":"val"}`
	if err := os.WriteFile(configPath, []byte(cfgData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm := NewConfigManager[testConfig](configPath)
	app.RegisterConfig("test", cm)

	rootCmd := &cobra.Command{Use: "cset-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config", "set", "name", "updated-name"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Fatalf("Type = %q, want %q", env.Type, TypeResult)
	}

	// Verify the set result envelope.
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatal("Data is not a map")
	}
	setVal, ok := dataMap["set"]
	if !ok {
		t.Fatal("Data missing 'set' key")
	}
	setMap, ok := setVal.(map[string]interface{})
	if !ok {
		t.Fatal("set value is not a map")
	}
	if setMap["path"] != "name" {
		t.Errorf("set.path = %q, want %q", setMap["path"], "name")
	}
	if setMap["value"] != "updated-name" {
		t.Errorf("set.value = %q, want %q", setMap["value"], "updated-name")
	}

	// Verify the file was actually updated.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var updated testConfig
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if updated.Name != "updated-name" {
		t.Errorf("config.Name = %q, want %q", updated.Name, "updated-name")
	}
}

// TestAgentConfigSetNotWhitelisted verifies that setting a non-whitelisted field fails.
func TestAgentConfigSetNotWhitelisted(t *testing.T) {
	app, buf := newTestApp("csetnw-tool", "1.0.0")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgData := `{"name":"original","api_key":"secret","port":3000,"rate":1.0,"debug":false,"internal":"val"}`
	if err := os.WriteFile(configPath, []byte(cfgData), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm := NewConfigManager[testConfig](configPath)
	app.RegisterConfig("test", cm)

	// "internal" has no config:"true" tag → not whitelisted.
	rootCmd := &cobra.Command{Use: "csetnw-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config", "set", "internal", "new-val"})

	code := app.Execute(rootCmd)
	if code != ExitInvalidParams {
		t.Fatalf("Execute() = %d, want %d", code, ExitInvalidParams)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "INPUT_INVALID" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "INPUT_INVALID")
	}
}

// TestAgentConfigSetNoProvider verifies that config set fails without a provider.
func TestAgentConfigSetNoProvider(t *testing.T) {
	app, buf := newTestApp("csetnp-tool", "1.0.0")

	rootCmd := &cobra.Command{Use: "csetnp-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config", "set", "name", "value"})

	code := app.Execute(rootCmd)
	if code != ExitInvalidParams {
		t.Fatalf("Execute() = %d, want %d", code, ExitInvalidParams)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "INPUT_INVALID" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "INPUT_INVALID")
	}
}

// TestAgentDoctorAllPass verifies doctor reports all sandbox checks as pass
// when directories are healthy.
func TestAgentDoctorAllPass(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DOCTOR_PASS_TEST_HOME", tmpDir)
	defer os.Unsetenv("DOCTOR_PASS_TEST_HOME")

	app, buf := newTestApp("doctor-pass-test", "1.0.0")
	// Override sandbox to use temp dir.
	app.sandbox = NewSandbox("doctor-pass-test")
	app.sandbox.Ensure()

	rootCmd := &cobra.Command{Use: "doctor-pass-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "doctor"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}

	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatal("Data is not a map")
	}
	checksRaw, ok := dataMap["checks"]
	if !ok {
		t.Fatal("Data missing 'checks' key")
	}
	checks, ok := checksRaw.([]interface{})
	if !ok {
		t.Fatal("checks is not a slice")
	}

	// All 4 sandbox dirs should pass.
	for _, c := range checks {
		check := c.(map[string]interface{})
		name := check["name"].(string)
		status := check["status"].(string)
		if strings.HasPrefix(name, "sandbox_") && status != "pass" {
			t.Errorf("check %q status = %q, want %q", name, status, "pass")
		}
	}
}

// TestAgentDoctorWithHealthChecks verifies that registered health checks appear in output.
func TestAgentDoctorWithHealthChecks(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DOCTOR_HC_TEST_HOME", tmpDir)
	defer os.Unsetenv("DOCTOR_HC_TEST_HOME")

	app, buf := newTestApp("doctor-hc-test", "1.0.0")
	app.sandbox = NewSandbox("doctor-hc-test")
	app.sandbox.Ensure()

	// Register a custom health check.
	app.RegisterHealthCheck("db_conn", func() HealthCheckResult {
		return HealthCheckResult{
			Name:    "db_conn",
			Status:  HealthCheckPass,
			Message: "database connection ok",
		}
	})
	app.RegisterHealthCheck("redis", func() HealthCheckResult {
		return HealthCheckResult{
			Name:    "redis",
			Status:  HealthCheckWarning,
			Message: "redis latency high",
		}
	})

	rootCmd := &cobra.Command{Use: "doctor-hc-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "doctor"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	dataMap := env.Data.(map[string]interface{})
	checks := dataMap["checks"].([]interface{})

	// Find custom checks.
	foundDB := false
	foundRedis := false
	for _, c := range checks {
		check := c.(map[string]interface{})
		name := check["name"].(string)
		if name == "db_conn" {
			foundDB = true
			if check["status"] != "pass" {
				t.Errorf("db_conn status = %q, want %q", check["status"], "pass")
			}
		}
		if name == "redis" {
			foundRedis = true
			if check["status"] != "warning" {
				t.Errorf("redis status = %q, want %q", check["status"], "warning")
			}
		}
	}
	if !foundDB {
		t.Error("custom health check 'db_conn' not found in output")
	}
	if !foundRedis {
		t.Error("custom health check 'redis' not found in output")
	}
}

// TestAgentDoctorSandboxMissing verifies that doctor reports fail for missing dirs.
func TestAgentDoctorSandboxMissing(t *testing.T) {
	// Use a temp dir that is never Ensure'd — sandbox dirs won't exist.
	tmpDir := t.TempDir()
	missingBase := filepath.Join(tmpDir, "nonexistent-sandbox")
	os.Setenv("DOCTOR_MISS_TEST_HOME", missingBase)
	defer os.Unsetenv("DOCTOR_MISS_TEST_HOME")

	app, buf := newTestApp("doctor-miss-test", "1.0.0")
	app.sandbox = NewSandbox("doctor-miss-test")
	// Deliberately NOT calling app.sandbox.Ensure()

	rootCmd := &cobra.Command{Use: "doctor-miss-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "doctor"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d (doctor always succeeds)", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	dataMap := env.Data.(map[string]interface{})
	checks := dataMap["checks"].([]interface{})

	// All sandbox checks should show fail since dirs don't exist.
	// Exception: sandbox_logs is always created by New() via Logger initialization.
	for _, c := range checks {
		check := c.(map[string]interface{})
		name := check["name"].(string)
		if strings.HasPrefix(name, "sandbox_") {
			status := check["status"].(string)
			if name == "sandbox_logs" {
				// Logs dir is created by New() via Logger initialization.
				if status != "pass" {
					t.Errorf("check %q status = %q, want %q (created by Logger in New())", name, status, "pass")
				}
				continue
			}
			if status != "fail" {
				t.Errorf("check %q status = %q, want %q (dir not created)", name, status, "fail")
			}
		}
	}
}

// --- T03: Debug last-crash with data, cache clean with files, integration tests ---

// TestAgentDebugLastCrashWithData writes a crash dump file and verifies
// that debug last-crash returns the crash dump data.
func TestAgentDebugLastCrashWithData(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DEBUG_DATA_TEST_HOME", tmpDir)
	defer os.Unsetenv("DEBUG_DATA_TEST_HOME")

	app, buf := newTestApp("debug-data-test", "1.0.0")
	app.sandbox = NewSandbox("debug-data-test")
	app.sandbox.Ensure()

	// Write a crash dump file directly.
	crashDir := app.sandbox.CrashDumpsDir()
	dumpData := map[string]interface{}{
		"timestamp":   "2025-01-15T10:30:00Z",
		"app_name":    "debug-data-test",
		"app_version": "1.0.0",
		"crash_type":  "signal",
		"signal":      "SIGTERM",
		"stack_trace": "goroutine 1 [running]:\nmain.main()",
		"flight_context": map[string]interface{}{
			"operation": "deploy",
		},
	}
	dumpJSON, _ := json.Marshal(dumpData)
	crashFile := filepath.Join(crashDir, "crash-20250115-103000.json")
	if err := os.WriteFile(crashFile, dumpJSON, 0644); err != nil {
		t.Fatalf("write crash dump: %v", err)
	}

	rootCmd := &cobra.Command{Use: "debug-data-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Fatalf("Type = %q, want %q", env.Type, TypeResult)
	}

	// Verify crash dump data.
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatal("Data is not a map")
	}
	if dataMap["crash_type"] != "signal" {
		t.Errorf("crash_type = %v, want %q", dataMap["crash_type"], "signal")
	}
	if dataMap["signal"] != "SIGTERM" {
		t.Errorf("signal = %v, want %q", dataMap["signal"], "SIGTERM")
	}
	if dataMap["app_name"] != "debug-data-test" {
		t.Errorf("app_name = %v, want %q", dataMap["app_name"], "debug-data-test")
	}
}

// TestAgentDebugLastCrashEmpty verifies that debug last-crash emits NOT_FOUND
// when the crash_dumps directory is empty.
func TestAgentDebugLastCrashEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DEBUG_EMPTY_TEST_HOME", tmpDir)
	defer os.Unsetenv("DEBUG_EMPTY_TEST_HOME")

	app, buf := newTestApp("debug-empty-test", "1.0.0")
	app.sandbox = NewSandbox("debug-empty-test")
	app.sandbox.Ensure()

	// Ensure crash dumps dir exists but is empty.
	crashDir := app.sandbox.CrashDumpsDir()
	os.MkdirAll(crashDir, 0755)

	rootCmd := &cobra.Command{Use: "debug-empty-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})

	code := app.Execute(rootCmd)
	if code != ExitNotFound {
		t.Errorf("Execute() = %d, want %d", code, ExitNotFound)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "NOT_FOUND" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "NOT_FOUND")
	}
}

// TestAgentDebugLastCrashIgnoresTmpFiles verifies that .tmp files are
// filtered out when looking for crash dumps.
func TestAgentDebugLastCrashIgnoresTmpFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DEBUG_TMP_TEST_HOME", tmpDir)
	defer os.Unsetenv("DEBUG_TMP_TEST_HOME")

	app, buf := newTestApp("debug-tmp-test", "1.0.0")
	app.sandbox = NewSandbox("debug-tmp-test")
	app.sandbox.Ensure()

	crashDir := app.sandbox.CrashDumpsDir()
	// Write only a .tmp file (partial write) — should be ignored.
	tmpFile := filepath.Join(crashDir, "crash-20250115-120000.json.tmp")
	if err := os.WriteFile(tmpFile, []byte(`{"partial":true}`), 0644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	rootCmd := &cobra.Command{Use: "debug-tmp-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})

	code := app.Execute(rootCmd)
	if code != ExitNotFound {
		t.Errorf("Execute() = %d, want %d (should ignore .tmp files)", code, ExitNotFound)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.ErrorCode != "NOT_FOUND" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "NOT_FOUND")
	}
}

// TestAgentDebugLastCrashNewestFirst verifies that when multiple crash dumps
// exist, the most recent one is returned.
func TestAgentDebugLastCrashNewestFirst(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DEBUG_NEWEST_TEST_HOME", tmpDir)
	defer os.Unsetenv("DEBUG_NEWEST_TEST_HOME")

	app, buf := newTestApp("debug-newest-test", "1.0.0")
	app.sandbox = NewSandbox("debug-newest-test")
	app.sandbox.Ensure()

	crashDir := app.sandbox.CrashDumpsDir()

	// Write an older crash dump.
	olderDump := map[string]interface{}{
		"timestamp":  "2025-01-10T08:00:00Z",
		"app_name":   "debug-newest-test",
		"crash_type": "signal",
		"signal":     "SIGTERM",
		"stack_trace": "old crash",
	}
	olderJSON, _ := json.Marshal(olderDump)
	olderFile := filepath.Join(crashDir, "crash-20250110-080000.json")
	if err := os.WriteFile(olderFile, olderJSON, 0644); err != nil {
		t.Fatalf("write older crash dump: %v", err)
	}

	// Write a newer crash dump (newer mtime — use a small sleep to ensure different mtime).
	newerDump := map[string]interface{}{
		"timestamp":  "2025-01-15T10:30:00Z",
		"app_name":   "debug-newest-test",
		"crash_type": "panic",
		"panic_value": "runtime error: index out of range",
		"stack_trace": "new crash",
	}
	newerJSON, _ := json.Marshal(newerDump)
	newerFile := filepath.Join(crashDir, "crash-20250115-103000.json")
	// Ensure different modification time.
	os.Chtimes(olderFile, time.Now().Add(-2*time.Second), time.Now().Add(-2*time.Second))
	if err := os.WriteFile(newerFile, newerJSON, 0644); err != nil {
		t.Fatalf("write newer crash dump: %v", err)
	}

	rootCmd := &cobra.Command{Use: "debug-newest-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	dataMap := env.Data.(map[string]interface{})
	// The newest crash should be the panic, not the signal crash.
	if dataMap["crash_type"] != "panic" {
		t.Errorf("crash_type = %v, want %q (should return newest)", dataMap["crash_type"], "panic")
	}
}

// TestAgentCacheCleanRemovesFiles verifies that cache clean removes files
// from the cache directory.
func TestAgentCacheCleanRemovesFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("CACHE_CLEAN_TEST_HOME", tmpDir)
	defer os.Unsetenv("CACHE_CLEAN_TEST_HOME")

	app, buf := newTestApp("cache-clean-test", "1.0.0")
	app.sandbox = NewSandbox("cache-clean-test")
	app.sandbox.Ensure()

	// Create files in the cache directory.
	cacheDir := app.sandbox.CacheDir()
	for i := 0; i < 3; i++ {
		f, err := os.Create(filepath.Join(cacheDir, fmt.Sprintf("cache-file-%d.tmp", i)))
		if err != nil {
			t.Fatalf("create cache file %d: %v", i, err)
		}
		f.WriteString("cached data")
		f.Close()
	}
	// Create a subdirectory too.
	os.MkdirAll(filepath.Join(cacheDir, "subdir"), 0755)

	// Verify files exist.
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 4 {
		t.Fatalf("setup: expected 4 entries in cache dir, got %d", len(entries))
	}

	rootCmd := &cobra.Command{Use: "cache-clean-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "cache", "clean"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}

	dataMap := env.Data.(map[string]interface{})
	cleaned := int(dataMap["cleaned"].(float64))
	if cleaned != 4 {
		t.Errorf("cleaned = %d, want 4", cleaned)
	}

	// Verify cache dir is empty but still exists.
	entries, _ = os.ReadDir(cacheDir)
	if len(entries) != 0 {
		t.Errorf("cache dir has %d entries after clean, want 0", len(entries))
	}

	// Verify the directory itself still exists.
	if info, err := os.Stat(cacheDir); err != nil || !info.IsDir() {
		t.Error("cache directory should still exist after clean")
	}
}

// TestAgentCacheCleanNonexistentDir verifies that cache clean succeeds
// when the cache directory doesn't exist.
func TestAgentCacheCleanNonexistentDir(t *testing.T) {
	tmpDir := t.TempDir()
	missingBase := filepath.Join(tmpDir, "nonexistent-cache")
	os.Setenv("CACHE_MISS_TEST_HOME", missingBase)
	defer os.Unsetenv("CACHE_MISS_TEST_HOME")

	app, buf := newTestApp("cache-miss-test", "1.0.0")
	app.sandbox = NewSandbox("cache-miss-test")
	// Deliberately NOT calling Ensure() — cache dir doesn't exist.

	rootCmd := &cobra.Command{Use: "cache-miss-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "cache", "clean"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}

	dataMap := env.Data.(map[string]interface{})
	cleaned := int(dataMap["cleaned"].(float64))
	if cleaned != 0 {
		t.Errorf("cleaned = %d, want 0 (dir doesn't exist)", cleaned)
	}
}

// TestAgentIntegrationAllCommands runs all 6 agent commands end-to-end
// and verifies each emits valid JSONL that passes ValidateEnvelope.
func TestAgentIntegrationAllCommands(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("INTEGRATION_TEST_HOME", tmpDir)
	defer os.Unsetenv("INTEGRATION_TEST_HOME")

	app := New("integration-tool", "3.0.0")
	intBuf := &bytes.Buffer{}
	app.SetWriter(NewWriter(intBuf, "integration-tool"))
	app.sandbox = NewSandbox("integration-tool")
	app.sandbox.Ensure()

	// Register a config provider.
	configPath := filepath.Join(tmpDir, "config.json")
	cfgData := `{"name":"int-test","api_key":"secret-int","port":9090,"rate":2.5,"debug":true,"internal":"val"}`
	os.WriteFile(configPath, []byte(cfgData), 0644)
	cm := NewConfigManager[testConfig](configPath)
	app.RegisterConfig("integration", cm)

	// Register a custom error code.
	app.RegisterErrorCode("TEST_ERR", 10, "integration test error")

	// Register a health check.
	app.RegisterHealthCheck("integration_check", func() HealthCheckResult {
		return HealthCheckResult{
			Name:    "integration_check",
			Status:  HealthCheckPass,
			Message: "all good",
		}
	})

	// Register command meta.
	app.RegisterCommandMeta("deploy", CommandMeta{
		Description:  "Deploy the app",
		IsIdempotent: true,
	})

	// Create rootCmd with user commands + agent commands.
	rootCmd := &cobra.Command{Use: "integration-tool"}
	deployCmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy the application",
		Run:   func(cmd *cobra.Command, args []string) {},
	}
	rootCmd.AddCommand(deployCmd)
	rootCmd.AddCommand(app.AgentCommands())

	// Test each of the 6 agent command paths.
	tests := []struct {
		name        string
		args        []string
		wantType    string
		wantSuccess bool
	}{
		{
			name:        "schema",
			args:        []string{"agent", "schema"},
			wantType:    TypeResult,
			wantSuccess: true,
		},
		{
			name:        "errors",
			args:        []string{"agent", "errors"},
			wantType:    TypeResult,
			wantSuccess: true,
		},
		{
			name:        "config list",
			args:        []string{"agent", "config", "list"},
			wantType:    TypeResult,
			wantSuccess: true,
		},
		{
			name:        "config set",
			args:        []string{"agent", "config", "set", "name", "updated-int"},
			wantType:    TypeResult,
			wantSuccess: true,
		},
		{
			name:        "doctor",
			args:        []string{"agent", "doctor"},
			wantType:    TypeResult,
			wantSuccess: true,
		},
		{
			name:        "cache clean",
			args:        []string{"agent", "cache", "clean"},
			wantType:    TypeResult,
			wantSuccess: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			intBuf.Reset()

			rootCmd.SetArgs(tc.args)
			code := app.Execute(rootCmd)

			if tc.wantSuccess && code != ExitSuccess {
				t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
			}

			output := strings.TrimSpace(intBuf.String())
			env, err := parseEnvelope(output)
			if err != nil {
				t.Fatalf("parse envelope: %v\noutput: %s", err, output)
			}

			// Every command must produce a valid envelope.
			if err := ValidateEnvelope(env); err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}

			// Check the envelope type.
			if env.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", env.Type, tc.wantType)
			}

			// Verify tool name is present.
			if env.Tool != "integration-tool" {
				t.Errorf("Tool = %q, want %q", env.Tool, "integration-tool")
			}

			// Verify timestamp is present.
			if env.Timestamp == "" {
				t.Error("Timestamp is empty")
			}
		})
	}
}

// TestAgentDebugLastCrashWithMultipleDumps verifies that debug last-crash
// returns the newest dump by modification time, not by filename order.
func TestAgentDebugLastCrashWithMultipleDumps(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("DEBUG_MULTI_TEST_HOME", tmpDir)
	defer os.Unsetenv("DEBUG_MULTI_TEST_HOME")

	app, buf := newTestApp("debug-multi-test", "1.0.0")
	app.sandbox = NewSandbox("debug-multi-test")
	app.sandbox.Ensure()

	crashDir := app.sandbox.CrashDumpsDir()

	// Write three crash dumps with different mtimes.
	for _, tc := range []struct {
		filename string
		signal   string
		mtime    time.Time
	}{
		{"crash-20250110-080000.json", "SIGTERM", time.Now().Add(-3 * time.Hour)},
		{"crash-20250115-120000.json", "SIGINT", time.Now().Add(-1 * time.Hour)},
		{"crash-20250112-100000.json", "SIGSEGV", time.Now()}, // newest mtime
	} {
		dump := map[string]interface{}{
			"timestamp":   "2025-01-15T10:00:00Z",
			"app_name":    "debug-multi-test",
			"crash_type":  "signal",
			"signal":      tc.signal,
			"stack_trace": fmt.Sprintf("stack for %s", tc.signal),
		}
		dumpJSON, _ := json.Marshal(dump)
		fp := filepath.Join(crashDir, tc.filename)
		os.WriteFile(fp, dumpJSON, 0644)
		os.Chtimes(fp, tc.mtime, tc.mtime)
	}

	rootCmd := &cobra.Command{Use: "debug-multi-test"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "debug", "last-crash"})

	code := app.Execute(rootCmd)
	if code != ExitSuccess {
		t.Fatalf("Execute() = %d, want %d", code, ExitSuccess)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	dataMap := env.Data.(map[string]interface{})
	// Should return the SIGSEGV crash — newest mtime.
	if dataMap["signal"] != "SIGSEGV" {
		t.Errorf("signal = %v, want %q (newest by mtime)", dataMap["signal"], "SIGSEGV")
	}
}

// --- T02: errors.As() classification tests ---

// mockWhitelistProvider is a ConfigProvider whose Set method returns a
// third-party WhitelistError implementation. This verifies that
// agentConfigSetCmd classifies it as INPUT_INVALID via errors.As()
// without any string matching.
type mockWhitelistProvider struct{}

func (m *mockWhitelistProvider) ListRedacted() (interface{}, error) {
	return map[string]interface{}{}, nil
}

func (m *mockWhitelistProvider) Set(jsonPath, value string) error {
	return &thirdPartyWhitelistError{field: jsonPath}
}

func (m *mockWhitelistProvider) Whitelist() []string {
	return []string{}
}

// thirdPartyWhitelistError simulates a third-party ConfigProvider's
// error type that satisfies WhitelistError via the marker-method convention.
type thirdPartyWhitelistError struct {
	field string
}

func (e *thirdPartyWhitelistError) Error() string {
	return "custom provider: field " + e.field + " blocked by policy"
}

func (e *thirdPartyWhitelistError) Field() string         { return e.field }
func (e *thirdPartyWhitelistError) IsWhitelistError() bool { return true }

// TestAgentConfigSetClassifiesThirdPartyWhitelistError verifies that
// agentConfigSetCmd uses errors.As() (not strings.Contains) to classify
// a third-party WhitelistError as INPUT_INVALID.
func TestAgentConfigSetClassifiesThirdPartyWhitelistError(t *testing.T) {
	app, buf := newTestApp("thirdparty-wl-tool", "1.0.0")

	// Register a mock provider that returns a third-party WhitelistError.
	app.RegisterConfig("mock", &mockWhitelistProvider{})

	rootCmd := &cobra.Command{Use: "thirdparty-wl-tool"}
	rootCmd.AddCommand(app.AgentCommands())
	rootCmd.SetArgs([]string{"agent", "config", "set", "some_field", "value"})

	code := app.Execute(rootCmd)
	if code != ExitInvalidParams {
		t.Fatalf("Execute() = %d, want %d (INPUT_INVALID)", code, ExitInvalidParams)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "INPUT_INVALID" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "INPUT_INVALID")
	}
	// Verify the error message is from the third-party provider, not a generic one.
	if !strings.Contains(env.Message, "blocked by policy") {
		t.Errorf("Message = %q, want third-party error message containing 'blocked by policy'", env.Message)
	}
}

