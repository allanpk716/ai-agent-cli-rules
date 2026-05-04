package agentsdk

import (
	"os"
	"syscall"
	"testing"
)

func TestSignalHandlerStopCleansUp(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("SIGSTOPTEST_HOME", tmpDir)
	defer os.Unsetenv("SIGSTOPTEST_HOME")

	sandbox := NewSandbox("sig-stop-test")
	fc := NewFlightContext()

	stop := SetupSignalHandler("sig-stop-test", "1.0.0", "", sandbox, fc)

	// Calling stop should not panic.
	stop()
	// Calling stop again should also be safe.
	stop()
}

func TestSignalNameMapping(t *testing.T) {
	tests := []struct {
		sig  os.Signal
		want string
	}{
		{syscall.SIGTERM, "SIGTERM"},
		{os.Interrupt, "SIGINT"},
	}
	for _, tt := range tests {
		got := signalName(tt.sig)
		if got != tt.want {
			t.Errorf("signalName(%v) = %q, want %q", tt.sig, got, tt.want)
		}
	}
}
