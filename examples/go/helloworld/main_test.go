package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/allanpk716/agent-cli-sdk"
	"github.com/spf13/cobra"
)

// buildApp creates the helloworld app and root command wired to a buffer,
// returning the app, buffer, and rootCmd. Each test gets an isolated instance.
func buildApp() (*agentsdk.App, *strings.Builder, *cobra.Command) {
	app := agentsdk.New("hello-agent", "1.0.0")
	buf := &strings.Builder{}
	app.SetWriter(agentsdk.NewWriter(buf, "hello-agent"))

	// Config manager — each test uses a temp dir so there's no shared state.
	cfgMgr := agentsdk.NewConfigManager[helloConfig]("config.json")
	app.RegisterConfig("default", cfgMgr)

	rootCmd := &cobra.Command{
		Use:   "hello-agent",
		Short: "A hello-world agent CLI",
	}

	greetCmd := &cobra.Command{
		Use:   "greet [name]",
		Short: "Greet someone",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "world"
			if len(args) > 0 {
				name = args[0]
			}
			return app.JSONL().Success(map[string]string{
				"greeting": fmt.Sprintf("Hello, %s!", name),
			})
		},
	}

	failCmd := &cobra.Command{
		Use:   "fail",
		Short: "Demonstrate error output",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().ErrorWithCode("INPUT_INVALID", "this command always fails")
			return &agentsdk.ExitError{
				Code: agentsdk.ExitInvalidParams,
				Err:  fmt.Errorf("this command always fails"),
			}
		},
	}

	progressCmd := &cobra.Command{
		Use:   "progress",
		Short: "Demonstrate progress output",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().Progress(50, "halfway there")
			app.JSONL().Progress(100, "done")
			return app.JSONL().Success(map[string]string{"status": "complete"})
		},
	}

	warnCmd := &cobra.Command{
		Use:   "warn",
		Short: "Demonstrate warning output",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().Warning("something seems off")
			return app.JSONL().Success(map[string]string{"status": "warned"})
		},
	}

	panicCmd := &cobra.Command{
		Use:   "panic",
		Short: "Demonstrate panic recovery",
		Run: func(cmd *cobra.Command, args []string) {
			panic("intentional panic for demonstration")
		},
	}

	rootCmd.AddCommand(greetCmd, failCmd, progressCmd, warnCmd, panicCmd)
	rootCmd.AddCommand(app.AgentCommands())

	return app, buf, rootCmd
}

// runCmd is a test helper that sets args on rootCmd, executes via app.Execute,
// and returns the exit code and captured output.
func runCmd(app *agentsdk.App, rootCmd *cobra.Command, args ...string) (int, string) {
	buf := &strings.Builder{}
	app.SetWriter(agentsdk.NewWriter(buf, "hello-agent"))
	rootCmd.SetArgs(args)
	code := app.Execute(rootCmd)
	return code, buf.String()
}

// --- Tests ---

func TestHelloWorldGreet(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "greet", "Alice")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type result, got %q", env.Type)
	}
	// Data should contain "greeting": "Hello, Alice!"
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}
	if dataMap["greeting"] != "Hello, Alice!" {
		t.Fatalf("expected greeting 'Hello, Alice!', got %v", dataMap["greeting"])
	}
}

func TestHelloWorldGreetDefault(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "greet")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	dataMap := env.Data.(map[string]interface{})
	if dataMap["greeting"] != "Hello, world!" {
		t.Fatalf("expected greeting 'Hello, world!', got %v", dataMap["greeting"])
	}
}

func TestHelloWorldFail(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "fail")
	if code != agentsdk.ExitInvalidParams {
		t.Fatalf("expected exit code %d, got %d", agentsdk.ExitInvalidParams, code)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type error, got %q", env.Type)
	}
	if env.ErrorCode != "INPUT_INVALID" {
		t.Fatalf("expected error_code INPUT_INVALID, got %q", env.ErrorCode)
	}
}

func TestHelloWorldProgress(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "progress")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	envs := agentsdk.MustParseEnvelopes(t, output)
	if len(envs) != 3 {
		t.Fatalf("expected 3 envelopes, got %d", len(envs))
	}
	// First two should be progress, last should be result.
	for i, env := range envs[:2] {
		if err := agentsdk.ValidateEnvelope(env); err != nil {
			t.Fatalf("envelope %d validation failed: %v", i, err)
		}
		if env.Type != agentsdk.TypeProgress {
			t.Fatalf("envelope %d: expected type progress, got %q", i, env.Type)
		}
	}
	if envs[2].Type != agentsdk.TypeResult {
		t.Fatalf("last envelope: expected type result, got %q", envs[2].Type)
	}
}

func TestHelloWorldWarning(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "warn")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	envs := agentsdk.MustParseEnvelopes(t, output)
	if len(envs) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(envs))
	}
	if err := agentsdk.ValidateEnvelope(envs[0]); err != nil {
		t.Fatalf("warning envelope validation failed: %v", err)
	}
	if envs[0].Type != agentsdk.TypeWarning {
		t.Fatalf("expected first envelope type warning, got %q", envs[0].Type)
	}
	if envs[0].Message != "something seems off" {
		t.Fatalf("expected warning message 'something seems off', got %q", envs[0].Message)
	}
	if envs[1].Type != agentsdk.TypeResult {
		t.Fatalf("expected second envelope type result, got %q", envs[1].Type)
	}
}

func TestHelloWorldPanicRecovery(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "panic")
	if code != agentsdk.ExitFatalError {
		t.Fatalf("expected exit code %d, got %d", agentsdk.ExitFatalError, code)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if env.Type != agentsdk.TypeError {
		t.Fatalf("expected type error, got %q", env.Type)
	}
	if env.ErrorCode != "FATAL_CRASH" {
		t.Fatalf("expected error_code FATAL_CRASH, got %q", env.ErrorCode)
	}
	if !strings.Contains(env.Message, "intentional panic for demonstration") {
		t.Fatalf("expected message to contain panic text, got %q", env.Message)
	}
}

func TestHelloWorldQuietMode(t *testing.T) {
	app, _, rootCmd := buildApp()

	// With --quiet, progress and warning envelopes should be suppressed.
	code, output := runCmd(app, rootCmd, "--quiet", "progress")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	envs := agentsdk.MustParseEnvelopes(t, output)
	// Only the final result envelope should survive quiet mode.
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope in quiet mode, got %d", len(envs))
	}
	if envs[0].Type != agentsdk.TypeResult {
		t.Fatalf("expected result envelope, got %q", envs[0].Type)
	}
}

func TestHelloWorldQuietModeWarning(t *testing.T) {
	app, _, rootCmd := buildApp()

	// With --quiet, warning should be filtered too.
	code, output := runCmd(app, rootCmd, "--quiet", "warn")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	envs := agentsdk.MustParseEnvelopes(t, output)
	if len(envs) != 1 {
		t.Fatalf("expected 1 envelope in quiet mode, got %d", len(envs))
	}
	if envs[0].Type != agentsdk.TypeResult {
		t.Fatalf("expected result envelope, got %q", envs[0].Type)
	}
}

func TestHelloWorldAgentSchema(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "agent", "schema")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type result, got %q", env.Type)
	}
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}
	if dataMap["tool"] != "hello-agent" {
		t.Fatalf("expected tool 'hello-agent', got %v", dataMap["tool"])
	}
	commands, ok := dataMap["commands"].([]interface{})
	if !ok {
		t.Fatalf("expected commands to be an array, got %T", dataMap["commands"])
	}
	if len(commands) == 0 {
		t.Fatal("expected at least one command in schema")
	}
}

func TestHelloWorldAgentErrors(t *testing.T) {
	app, _, rootCmd := buildApp()

	code, output := runCmd(app, rootCmd, "agent", "errors")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type result, got %q", env.Type)
	}
	dataMap, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be a map, got %T", env.Data)
	}
	codes, ok := dataMap["codes"].([]interface{})
	if !ok {
		t.Fatalf("expected codes to be an array, got %T", dataMap["codes"])
	}
	if len(codes) == 0 {
		t.Fatal("expected at least one error code in output")
	}
}

func TestHelloWorldConfigList(t *testing.T) {
	app, _, rootCmd := buildApp()

	// Create a temp config file with a sensitive field for redaction testing.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{"name":"test-agent","language":"en","api_key":"super-secret-key"}`
	if err := os.WriteFile(cfgPath, []byte(configJSON), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Replace the config manager with one pointing at the temp file.
	cfgMgr := agentsdk.NewConfigManager[helloConfig](cfgPath)
	app.RegisterConfig("default", cfgMgr)

	code, output := runCmd(app, rootCmd, "agent", "config", "list")
	if code != agentsdk.ExitSuccess {
		t.Fatalf("expected exit code 0, got %d; output: %s", code, output)
	}

	env := agentsdk.MustParseEnvelope(t, strings.TrimSpace(output))
	if err := agentsdk.ValidateEnvelope(env); err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if env.Type != agentsdk.TypeResult {
		t.Fatalf("expected type result, got %q", env.Type)
	}

	// Parse nested data to check redaction.
	rawData, _ := json.Marshal(env.Data)
	var parsed struct {
		Config struct {
			Name     string `json:"name"`
			Language string `json:"language"`
			APIKey   string `json:"api_key"`
		} `json:"config"`
	}
	if err := json.Unmarshal(rawData, &parsed); err != nil {
		t.Fatalf("parse config data: %v", err)
	}
	if parsed.Config.Name != "test-agent" {
		t.Fatalf("expected name 'test-agent', got %q", parsed.Config.Name)
	}
	if parsed.Config.APIKey != "***" {
		t.Fatalf("expected api_key to be redacted to '***', got %q", parsed.Config.APIKey)
	}
}
