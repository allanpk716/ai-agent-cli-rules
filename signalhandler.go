package agentsdk

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// SignalHandlerConfig controls the behavior of SetupSignalHandler.
// This allows tests to override os.Exit and other side effects.
type SignalHandlerConfig struct {
	// OnSignal is called when a signal is received, after the crash dump is written.
	// Defaults to os.Exit(1) if nil.
	OnSignal func()

	// OnWriteError is called when the crash dump write fails.
	// Defaults to fmt.Fprintf(os.Stderr, ...) if nil.
	OnWriteError func(err error)
}

// SetupSignalHandler installs a goroutine that listens for SIGTERM and SIGINT.
// On signal receipt, it captures a FlightContext snapshot, writes a crash dump
// file via WriteCrashDump, logs any write failure to stderr (best-effort), and
// calls os.Exit(1).
//
// It returns a stop function that should be called on normal exit to clean up
// the signal channel. Calling the stop function after the goroutine has already
// exited (due to a signal) is safe — signal.Stop is idempotent.
func SetupSignalHandler(appName, appVersion, traceID string, sandbox *Sandbox, fc *FlightContext) (stop func()) {
	return SetupSignalHandlerWithConfig(appName, appVersion, traceID, sandbox, fc, SignalHandlerConfig{})
}

// SetupSignalHandlerWithConfig is like SetupSignalHandler but allows overriding
// the exit and error behavior for testing.
func SetupSignalHandlerWithConfig(appName, appVersion, traceID string, sandbox *Sandbox, fc *FlightContext, cfg SignalHandlerConfig) (stop func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGTERM, os.Interrupt)

	onSignal := cfg.OnSignal
	if onSignal == nil {
		onSignal = func() { os.Exit(1) }
	}
	onWriteError := cfg.OnWriteError
	if onWriteError == nil {
		onWriteError = func(err error) {
			fmt.Fprintf(os.Stderr, "crashdump: signal handler write failed: %v\n", err)
		}
	}

	stop = func() {
		signal.Stop(ch)
	}

	go func() {
		sig, ok := <-ch
		if !ok {
			return
		}

		// Capture stack trace for signal context.
		stackBuf := make([]byte, 4096)
		n := runtime.Stack(stackBuf, false)
		stackTrace := string(stackBuf[:n])

		// Build and write crash dump.
		sigName := signalName(sig)
		dump := &CrashDump{
			Timestamp:     time.Now().Format(time.RFC3339),
			AppName:       appName,
			AppVersion:    appVersion,
			TraceID:       traceID,
			CrashType:     "signal",
			Signal:        sigName,
			StackTrace:    stackTrace,
			FlightContext: fc.Snapshot(),
		}

		if err := WriteCrashDump(sandbox, dump); err != nil {
			onWriteError(err)
		}

		onSignal()
	}()

	return stop
}

// signalName returns a human-readable signal name.
func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case os.Interrupt:
		return "SIGINT"
	default:
		return sig.String()
	}
}
