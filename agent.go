package agentsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ConfigProvider is the interface that ConfigManager[T] satisfies automatically.
// It enables config list/config set agent commands to operate on any registered
// config manager without knowing the concrete type parameter.
type ConfigProvider interface {
	// ListRedacted loads the config, redacts sensitive fields, and returns it
	// as a generic interface{} suitable for JSONL emission.
	ListRedacted() (interface{}, error)

	// Set loads the config, validates the jsonPath against the whitelist,
	// sets the value, validates, and saves.
	Set(jsonPath, value string) error

	// Whitelist returns the json tag names of settable fields.
	Whitelist() []string
}

// HealthCheckStatus represents the outcome of a health check.
type HealthCheckStatus string

const (
	// HealthCheckPass indicates the check succeeded.
	HealthCheckPass HealthCheckStatus = "pass"
	// HealthCheckFail indicates the check failed.
	HealthCheckFail HealthCheckStatus = "fail"
	// HealthCheckWarning indicates the check passed with a warning.
	HealthCheckWarning HealthCheckStatus = "warning"
)

// HealthCheckResult is the output of a single health check.
type HealthCheckResult struct {
	Name    string            `json:"name"`
	Status  HealthCheckStatus `json:"status"`
	Message string            `json:"message,omitempty"`
	Details interface{}       `json:"details,omitempty"`
}

// HealthCheckFunc is a function that performs a health check and returns a result.
type HealthCheckFunc func() HealthCheckResult

// CommandMeta enriches a Cobra command's schema entry with description and
// idempotency metadata. Users register this via app.RegisterCommandMeta().
type CommandMeta struct {
	Description  string `json:"description,omitempty"`
	IsIdempotent bool   `json:"is_idempotent,omitempty"`
}

// commandEntry is a single command's schema representation for JSONL output.
type commandEntry struct {
	Name         string      `json:"name"`
	Description  string      `json:"description,omitempty"`
	IsIdempotent bool        `json:"is_idempotent,omitempty"`
	Flags        []flagEntry `json:"flags,omitempty"`
	Args         bool        `json:"accepts_args,omitempty"`
}

// flagEntry is a single flag's schema representation.
type flagEntry struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// schemaOutput is the top-level data payload for agent schema.
type schemaOutput struct {
	Tool     string         `json:"tool"`
	Version  string         `json:"version"`
	Commands []commandEntry `json:"commands"`
}

// errorEntry is a single error code's representation for JSONL output.
type errorEntry struct {
	Code        string `json:"code"`
	ExitCode    int    `json:"exit_code"`
	Description string `json:"description"`
}

// errorsOutput is the top-level data payload for agent errors.
type errorsOutput struct {
	Codes []errorEntry `json:"codes"`
	Count int          `json:"count"`
}

// AgentCommands returns a *cobra.Command tree with Use "agent" containing
// 6 sub-commands: schema, errors, config, doctor, debug, cache.
// Users add this to their rootCmd to enable all agent meta-commands.
func (a *App) AgentCommands() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent introspection and diagnostic commands",
	}

	cmd.AddCommand(a.agentSchemaCmd())
	cmd.AddCommand(a.agentErrorsCmd())
	cmd.AddCommand(a.agentConfigCmd())
	cmd.AddCommand(a.agentDoctorCmd())
	cmd.AddCommand(a.agentDebugCmd())
	cmd.AddCommand(a.agentCacheCmd())

	return cmd
}

// agentSchemaCmd implements "agent schema" — walks the Cobra command tree,
// merges with CommandMeta registrations, and emits JSONL result.
func (a *App) agentSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the command schema for this tool",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			commands := walkCommands(root, "")

			// Merge CommandMeta registrations.
			for i := range commands {
				key := commands[i].Name
				if meta, ok := a.commandMeta[key]; ok {
					if meta.Description != "" {
						commands[i].Description = meta.Description
					}
					if meta.IsIdempotent {
						commands[i].IsIdempotent = true
					}
				}
			}

			data := schemaOutput{
				Tool:     a.name,
				Version:  a.version,
				Commands: commands,
			}

			return a.writer.Success(data)
		},
	}
}

// agentErrorsCmd implements "agent errors" — emits all registered error codes.
func (a *App) agentErrorsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "errors",
		Short: "List all registered error codes",
		RunE: func(cmd *cobra.Command, args []string) error {
			allCodes := a.registry.AllCodes()
			var codes []errorEntry
			for code, entry := range allCodes {
				codes = append(codes, errorEntry{
					Code:        code,
					ExitCode:    entry.ExitCode,
					Description: entry.Description,
				})
			}

			// Sort for deterministic output.
			// Go maps have random iteration order.
			sortErrorEntries(codes)

			data := errorsOutput{
				Codes: codes,
				Count: len(codes),
			}

			return a.writer.Success(data)
		},
	}
}

// agentConfigCmd returns a parent "config" command with "list" and "set" sub-commands.
func (a *App) agentConfigCmd() *cobra.Command {
	config := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
	}

	config.AddCommand(a.agentConfigListCmd())
	config.AddCommand(a.agentConfigSetCmd())

	return config
}

// agentConfigListCmd implements "agent config list" — emits the redacted config via the registered provider.
func (a *App) agentConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List current configuration (redacted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := a.firstConfigProvider()
			if provider == nil {
				a.writer.ErrorWithCode("INPUT_INVALID", "no config provider registered")
				return &ExitError{Code: ExitInvalidParams, Err: fmt.Errorf("no config provider registered")}
			}

			data, err := provider.ListRedacted()
			if err != nil {
				a.writer.ErrorWithCode("INTERNAL_ERROR", fmt.Sprintf("config list: %v", err))
				return &ExitError{Code: ExitFatalError, Err: err}
			}

			return a.writer.Success(map[string]interface{}{
				"config": data,
			})
		},
	}
}

// agentConfigSetCmd implements "agent config set <json_path> <value>" — sets a config field.
func (a *App) agentConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := a.firstConfigProvider()
			if provider == nil {
				a.writer.ErrorWithCode("INPUT_INVALID", "no config provider registered")
				return &ExitError{Code: ExitInvalidParams, Err: fmt.Errorf("no config provider registered")}
			}

			err := provider.Set(args[0], args[1])
			if err != nil {
				// Distinguish whitelist violation from internal error.
				errMsg := err.Error()
				if strings.Contains(errMsg, "not configurable") || strings.Contains(errMsg, "not in whitelist") || strings.Contains(errMsg, "unknown field") {
					a.writer.ErrorWithCode("INPUT_INVALID", errMsg)
					return &ExitError{Code: ExitInvalidParams, Err: err}
				}
				a.writer.ErrorWithCode("INTERNAL_ERROR", errMsg)
				return &ExitError{Code: ExitFatalError, Err: err}
			}

			return a.writer.Success(map[string]interface{}{
				"set": map[string]string{
					"path":  args[0],
					"value": args[1],
				},
			})
		},
	}
}

// firstConfigProvider returns the first registered config provider, or nil if none registered.
func (a *App) firstConfigProvider() ConfigProvider {
	for _, p := range a.configProviders {
		return p
	}
	return nil
}

// agentDoctorCmd runs built-in sandbox health checks plus any registered health checks.
func (a *App) agentDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			var checks []HealthCheckResult

			// Built-in check: sandbox directories.
			for name, path := range a.sandbox.Dirs() {
				exists := false
				writable := false
				var msg string
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					exists = true
					tmpFile := filepath.Join(path, ".doctor-check")
					if f, err := os.Create(tmpFile); err == nil {
						f.Close()
						os.Remove(tmpFile)
						writable = true
					}
				}
				if exists && writable {
					msg = "directory exists and is writable"
				} else if exists {
					msg = "directory exists but is not writable"
				} else {
					msg = "directory does not exist"
				}
				status := HealthCheckPass
				if !exists || !writable {
					status = HealthCheckFail
				}
				checks = append(checks, HealthCheckResult{
					Name:    fmt.Sprintf("sandbox_%s", name),
					Status:  status,
					Message: msg,
					Details: map[string]interface{}{
						"path":     path,
						"exists":   exists,
						"writable": writable,
					},
				})
			}

			// Built-in check: config file (if provider registered).
			if provider := a.firstConfigProvider(); provider != nil {
				// Access the config file path via the first registered provider.
				// We check if a config file can be loaded.
				_, err := provider.ListRedacted()
				if err == nil {
					checks = append(checks, HealthCheckResult{
						Name:    "config_file",
						Status:  HealthCheckPass,
						Message: "configuration file is readable",
					})
				} else {
					checks = append(checks, HealthCheckResult{
						Name:    "config_file",
						Status:  HealthCheckFail,
						Message: fmt.Sprintf("configuration file check failed: %v", err),
					})
				}
			}

			// User-registered health checks.
			for name, fn := range a.healthChecks {
				result := fn()
				// Ensure name matches the registration key if not set.
				if result.Name == "" {
					result.Name = name
				}
				checks = append(checks, result)
			}

			return a.writer.Success(map[string]interface{}{
				"checks": checks,
			})
		},
	}
}

// agentDebugCmd is a parent for debug sub-commands.
func (a *App) agentDebugCmd() *cobra.Command {
	debug := &cobra.Command{
		Use:   "debug",
		Short: "Debug and diagnostic commands",
	}

	debug.AddCommand(a.agentDebugLastCrashCmd())

	return debug
}

// agentDebugLastCrashCmd implements "agent debug last-crash" — reads the most
// recent .json crash dump from the crash_dumps directory.
func (a *App) agentDebugLastCrashCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "last-crash",
		Short: "Show the most recent crash dump",
		RunE: func(cmd *cobra.Command, args []string) error {
			crashDir := a.sandbox.CrashDumpsDir()
			entries, err := os.ReadDir(crashDir)
			if err != nil || len(entries) == 0 {
				a.writer.ErrorWithCode("NOT_FOUND", "no crash dumps found")
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("no crash dumps found")}
			}

			// Filter to only .json files (skip .tmp partial writes).
			var jsonFiles []os.DirEntry
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					jsonFiles = append(jsonFiles, e)
				}
			}
			if len(jsonFiles) == 0 {
				a.writer.ErrorWithCode("NOT_FOUND", "no crash dumps found")
				return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("no crash dumps found")}
			}

			// Sort by modification time, newest first.
			sort.Slice(jsonFiles, func(i, j int) bool {
				fi, err := jsonFiles[i].Info()
				if err != nil {
					return false
				}
				fj, err := jsonFiles[j].Info()
				if err != nil {
					return false
				}
				return fi.ModTime().After(fj.ModTime())
			})

			// Read the most recent crash dump.
			lastEntry := jsonFiles[0]
			data, err := os.ReadFile(filepath.Join(crashDir, lastEntry.Name()))
			if err != nil {
				a.writer.ErrorWithCode("INTERNAL_ERROR", fmt.Sprintf("read crash dump: %v", err))
				return &ExitError{Code: ExitFatalError, Err: err}
			}

			// Parse to validate it's proper JSON, then emit as result.
			var dump map[string]interface{}
			if err := parseJSON(data, &dump); err != nil {
				a.writer.ErrorWithCode("INTERNAL_ERROR", fmt.Sprintf("parse crash dump: %v", err))
				return &ExitError{Code: ExitFatalError, Err: err}
			}

			return a.writer.Success(dump)
		},
	}
}

// agentCacheCmd returns a parent "cache" command with a "clean" sub-command.
func (a *App) agentCacheCmd() *cobra.Command {
	cache := &cobra.Command{
		Use:   "cache",
		Short: "Cache management commands",
	}

	cache.AddCommand(a.agentCacheCleanCmd())

	return cache
}

// agentCacheCleanCmd implements "agent cache clean" — removes all files in the
// cache directory but keeps the directory itself.
func (a *App) agentCacheCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove all cached files",
		RunE: func(cmd *cobra.Command, args []string) error {
			cacheDir := a.sandbox.CacheDir()
			entries, err := os.ReadDir(cacheDir)
			if err != nil {
				if os.IsNotExist(err) {
					// Cache dir doesn't exist — nothing to clean, still succeed.
					return a.writer.Success(map[string]interface{}{
						"cleaned":   0,
						"cache_dir": cacheDir,
					})
				}
				a.writer.ErrorWithCode("INTERNAL_ERROR", fmt.Sprintf("read cache dir: %v", err))
				return &ExitError{Code: ExitFatalError, Err: err}
			}

			cleaned := 0
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(cacheDir, entry.Name())); err == nil {
					cleaned++
				}
			}

			return a.writer.Success(map[string]interface{}{
				"cleaned":   cleaned,
				"cache_dir": cacheDir,
			})
		},
	}
}

// walkCommands recursively walks the Cobra command tree and builds schema entries.
func walkCommands(cmd *cobra.Command, parentPath string) []commandEntry {
	var entries []commandEntry

	for _, sub := range cmd.Commands() {
		// Skip help command (auto-added by Cobra).
		if sub.Name() == "help" {
			continue
		}

		cmdPath := sub.Name()
		if parentPath != "" {
			cmdPath = parentPath + " " + sub.Name()
		}

		entry := commandEntry{
			Name:        cmdPath,
			Description: sub.Short,
			Args:        sub.Args != nil,
		}

		// Extract flags.
		sub.Flags().VisitAll(func(f *pflag.Flag) {
			entry.Flags = append(entry.Flags, flagEntry{
				Name:        f.Name,
				Shorthand:   f.Shorthand,
				Type:        f.Value.Type(),
				Default:     f.DefValue,
				Description: f.Usage,
			})
		})

		// Recurse into subcommands.
		if sub.HasSubCommands() {
			children := walkCommands(sub, cmdPath)
			entries = append(entries, children...)
		}

		entries = append(entries, entry)
	}

	return entries
}

// sortErrorEntries sorts error entries by code for deterministic output.
func sortErrorEntries(entries []errorEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Code < entries[j].Code
	})
}

// parseJSON is a small helper to parse JSON bytes into a map.
func parseJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// formatDirAge returns a human-readable string for the file modification time.
func formatDirAge(modTime time.Time) string {
	age := time.Since(modTime)
	if age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(age.Hours()/24))
}
