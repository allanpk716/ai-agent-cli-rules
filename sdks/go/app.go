package agentsdk

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

// App is the main SDK entry point. It bundles a JSONL Writer, an ErrorCodeRegistry,
// a Sandbox for directory management, a FlightContext for crash black-box recording,
// and the Execute() method that runs a Cobra command tree with panic recovery,
// signal handling, and JSONL-native error reporting.
type App struct {
	name          string
	version       string
	writer        *Writer
	registry      *ErrorCodeRegistry
	sandbox       *Sandbox
	flightContext *FlightContext

	// Agent command registration maps.
	configProviders map[string]ConfigProvider
	healthChecks    map[string]HealthCheckFunc
	commandMeta     map[string]CommandMeta
}

// New creates a new App instance. The writer targets os.Stdout by default.
// If the AGENT_TRACE_ID environment variable is set, it is injected into
// all envelopes emitted by this app's writer.
func New(name, version string) *App {
	w := NewWriter(os.Stdout, name)
	if tid := os.Getenv("AGENT_TRACE_ID"); tid != "" {
		w.SetTraceID(tid)
	}
	return &App{
		name:            name,
		version:         version,
		writer:          w,
		registry:        NewErrorCodeRegistry(),
		sandbox:         NewSandbox(name),
		flightContext:   NewFlightContext(),
		configProviders: make(map[string]ConfigProvider),
		healthChecks:    make(map[string]HealthCheckFunc),
		commandMeta:     make(map[string]CommandMeta),
	}
}

// JSONL returns the JSONL Writer for producing structured output.
func (a *App) JSONL() *Writer {
	return a.writer
}

// Name returns the app name.
func (a *App) Name() string {
	return a.name
}

// Version returns the app version.
func (a *App) Version() string {
	return a.version
}

// Registry returns the app's ErrorCodeRegistry for external use.
// This allows client packages to inject the registry via constructor
// without needing a direct dependency on App internals.
func (a *App) Registry() *ErrorCodeRegistry {
	return a.registry
}

// Sandbox returns the app's Sandbox for directory management.
// The sandbox is created in New() based on the app name.
func (a *App) Sandbox() *Sandbox {
	return a.sandbox
}

// FlightContext returns the app's FlightContext for recording in-flight state.
// Values set in FlightContext will be captured in crash dumps on signal or panic.
func (a *App) FlightContext() *FlightContext {
	return a.flightContext
}

// RegisterErrorCode delegates to the app's ErrorCodeRegistry.
// Built-in codes (FATAL_CRASH, INTERNAL_ERROR, etc.) cannot be overridden.
func (a *App) RegisterErrorCode(code string, exitCode int, description string) error {
	return a.registry.Register(code, exitCode, description)
}

// RegisterConfig registers a named ConfigProvider for use by agent config commands.
func (a *App) RegisterConfig(name string, provider ConfigProvider) {
	a.configProviders[name] = provider
}

// RegisterHealthCheck registers a named health check function for use by agent doctor.
func (a *App) RegisterHealthCheck(name string, fn HealthCheckFunc) {
	a.healthChecks[name] = fn
}

// RegisterCommandMeta registers metadata enrichment for a command path.
// The cmdPath should match the command's full path (e.g. "deploy status").
func (a *App) RegisterCommandMeta(cmdPath string, meta CommandMeta) {
	a.commandMeta[cmdPath] = meta
}

// ErrorCodeToExitCode looks up the exit code for an error_code string.
// Returns ExitFatalError for unknown codes.
func (a *App) ErrorCodeToExitCode(code string) int {
	return a.registry.ToExitCode(code)
}

// SetWriter replaces the internal writer. Useful for testing with a
// bytes.Buffer instead of os.Stdout.
func (a *App) SetWriter(w *Writer) {
	a.writer = w
}

// Execute runs the rootCmd Cobra command tree with full panic recovery,
// signal handling, crash dump writing, and JSONL-native error handling.
// It returns an exit code (0-5).
//
// It registers a --quiet/-q flag, silences Cobra's default error output,
// sets up a signal handler for SIGTERM/SIGINT (writes crash dump then exits),
// and recovers from panics by writing a crash dump and emitting a FATAL_CRASH envelope.
func (a *App) Execute(rootCmd *cobra.Command) (code int) {
	// Register --quiet flag if not already present.
	if rootCmd.PersistentFlags().Lookup("quiet") == nil {
		rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Suppress progress and warning output")
	}

	// Silence Cobra's default error output — we handle errors via JSONL.
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	// Wire quiet flag to writer via PersistentPreRunE so it's read after
	// flag parsing but before the command runs.
	originalPreRunE := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")
		a.writer.SetQuiet(quiet)
		if originalPreRunE != nil {
			return originalPreRunE(cmd, args)
		}
		return nil
	}

	// Setup signal handler for SIGTERM/SIGINT — writes crash dump on signal.
	stopSignalHandler := SetupSignalHandler(
		a.name, a.version, a.writer.TraceID(),
		a.sandbox, a.flightContext,
	)
	defer stopSignalHandler()

	code = ExitSuccess

	// Top-level panic recovery — catches ALL panics from the command tree.
	defer func() {
		if r := recover(); r != nil {
			// Best-effort goroutine stack excerpt.
			// Use 4KB to keep output bounded.
			stackBuf := make([]byte, 4096)
			n := runtime.Stack(stackBuf, false)
			stackExcerpt := string(stackBuf[:n])

			// Write crash dump file (additive — does not replace FATAL_CRASH JSONL).
			dump := &CrashDump{
				Timestamp:     time.Now().Format(time.RFC3339),
				AppName:       a.name,
				AppVersion:    a.version,
				TraceID:       a.writer.TraceID(),
				CrashType:     "panic",
				PanicValue:    fmt.Sprintf("%v", r),
				StackTrace:    stackExcerpt,
				FlightContext: a.flightContext.Snapshot(),
			}
			if err := WriteCrashDump(a.sandbox, dump); err != nil {
				// Best-effort: log to stderr but don't block the JSONL emission.
				fmt.Fprintf(os.Stderr, "crashdump: panic write failed: %v\n", err)
			}

			// Emit FATAL_CRASH JSONL envelope (existing behavior preserved).
			msg := fmt.Sprintf("panic: %v\nStack:\n%s", r, stackExcerpt)
			a.writer.ErrorWithCode("FATAL_CRASH", msg)
			code = ExitFatalError
		}
	}()

	if err := rootCmd.Execute(); err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
			return
		}
		// Generic error — emit JSONL error and return fatal.
		a.writer.ErrorWithCode("INTERNAL_ERROR", err.Error())
		code = ExitFatalError
		return
	}

	code = ExitSuccess
	return
}
