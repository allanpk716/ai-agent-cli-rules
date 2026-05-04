// Package main demonstrates a minimal CLI built with the agent-cli-sdk.
// It shows the full SDK lifecycle: create App, register config, add custom
// and agent commands, and execute with JSONL output.
package main

import (
	"fmt"
	"os"

	agentsdk "github.com/allanpk716/agent-cli-sdk"
	"github.com/spf13/cobra"
)

// helloConfig is a sample configuration struct with struct tags that drive
// JSON serialization, sensitive field redaction, and whitelist-based setting.
type helloConfig struct {
	Name     string `json:"name"      config:"true"`
	Language string `json:"language"  config:"true"`
	APIKey   string `json:"api_key"   sensitive:"true"`
}

func main() {
	app := agentsdk.New("hello-agent", "1.0.0")

	// --- Config setup ---
	// ConfigManager reads/writes a JSON file. For this example the path is
	// derived from the HELLO_AGENT_HOME env var (set by tests).
	// In production you'd point this at a real config file path.
	configPath := "config.json"
	if cfgDir := app.Sandbox().DataDir(); cfgDir != "" {
		configPath = cfgDir + "/config.json"
	}
	cfgMgr := agentsdk.NewConfigManager[helloConfig](configPath)
	app.RegisterConfig("default", cfgMgr)

	// --- Root command ---
	rootCmd := &cobra.Command{
		Use:   "hello-agent",
		Short: "A hello-world agent CLI",
	}

	// greet command — emits a JSONL result envelope with a greeting.
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

	// fail command — demonstrates error output.
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

	// progress command — emits a progress sequence then a result.
	progressCmd := &cobra.Command{
		Use:   "progress",
		Short: "Demonstrate progress output",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().Progress(50, "halfway there")
			app.JSONL().Progress(100, "done")
			return app.JSONL().Success(map[string]string{"status": "complete"})
		},
	}

	// warn command — emits a warning and a result.
	warnCmd := &cobra.Command{
		Use:   "warn",
		Short: "Demonstrate warning output",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().Warning("something seems off")
			return app.JSONL().Success(map[string]string{"status": "warned"})
		},
	}

	// panic command — demonstrates panic recovery / FATAL_CRASH envelope.
	panicCmd := &cobra.Command{
		Use:   "panic",
		Short: "Demonstrate panic recovery",
		Run: func(cmd *cobra.Command, args []string) {
			panic("intentional panic for demonstration")
		},
	}

	rootCmd.AddCommand(greetCmd, failCmd, progressCmd, warnCmd, panicCmd)
	rootCmd.AddCommand(app.AgentCommands())

	os.Exit(app.Execute(rootCmd))
}
