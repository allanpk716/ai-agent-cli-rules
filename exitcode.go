package agentsdk

import "fmt"

// Semantic exit codes. Each code maps to a distinct failure category so that
// calling agents (shells, orchestrators) can branch on the numeric exit code.
const (
	ExitSuccess       = 0 // 正常完成
	ExitFatalError    = 1 // 未分类的致命错误
	ExitInvalidParams = 2 // 输入参数无效
	ExitNotFound      = 3 // 资源未找到
	ExitNetworkError  = 4 // 网络错误
	ExitLockConflict  = 5 // 资源互斥锁定
)

// ExitError wraps an error with a semantic exit code.
// It implements the error interface so it can be returned from any function
// that returns error, while carrying the exit code for os.Exit.
type ExitError struct {
	Code int
	Err  error
}

// Error returns "exit N: message" format for logging and display.
func (e *ExitError) Error() string {
	return fmt.Sprintf("exit %d: %s", e.Code, e.Err)
}

// Unwrap returns the wrapped error for errors.Is/As chaining.
func (e *ExitError) Unwrap() error {
	return e.Err
}

// errorCodeEntry is a registry entry mapping an error_code string to an
// exit code and human-readable description.
type errorCodeEntry struct {
	ExitCode    int
	Description string
}

// builtInErrorCodes are the default error codes that ship with the SDK.
// These cannot be overridden via Register.
var builtInErrorCodes = map[string]errorCodeEntry{
	"FATAL_CRASH":    {ExitCode: ExitFatalError, Description: "程序崩溃（panic 恢复）"},
	"INTERNAL_ERROR": {ExitCode: ExitFatalError, Description: "内部错误"},
	"INPUT_INVALID":  {ExitCode: ExitInvalidParams, Description: "输入参数无效"},
	"NOT_FOUND":      {ExitCode: ExitNotFound, Description: "资源未找到"},
	"RESOURCE_LOCKED": {ExitCode: ExitLockConflict, Description: "资源互斥锁定"},
}

// ErrorCodeRegistry maps error_code strings (used in JSONL envelopes) to
// semantic exit codes. Built-in codes are protected from override.
type ErrorCodeRegistry struct {
	codes map[string]errorCodeEntry
}

// NewErrorCodeRegistry creates a registry pre-loaded with built-in error codes.
func NewErrorCodeRegistry() *ErrorCodeRegistry {
	r := &ErrorCodeRegistry{
		codes: make(map[string]errorCodeEntry, len(builtInErrorCodes)),
	}
	for k, v := range builtInErrorCodes {
		r.codes[k] = v
	}
	return r
}

// Register adds a custom error code to the registry.
// Returns an error if the code conflicts with a built-in code.
func (r *ErrorCodeRegistry) Register(code string, exitCode int, description string) error {
	if _, isBuiltIn := builtInErrorCodes[code]; isBuiltIn {
		return fmt.Errorf("cannot override built-in error code: %s", code)
	}
	r.codes[code] = errorCodeEntry{ExitCode: exitCode, Description: description}
	return nil
}

// Lookup returns the exit code and description for the given error_code.
// ok is false if the code is not registered.
func (r *ErrorCodeRegistry) Lookup(code string) (exitCode int, description string, ok bool) {
	entry, ok := r.codes[code]
	if !ok {
		return 0, "", false
	}
	return entry.ExitCode, entry.Description, true
}

// ToExitCode returns the exit code for the given error_code string.
// Falls back to ExitFatalError (1) if the code is not found.
func (r *ErrorCodeRegistry) ToExitCode(code string) int {
	if entry, ok := r.codes[code]; ok {
		return entry.ExitCode
	}
	return ExitFatalError
}

// AllCodes returns a copy of all registered codes (built-in + custom).
// Modifications to the returned map do not affect the registry.
func (r *ErrorCodeRegistry) AllCodes() map[string]errorCodeEntry {
	out := make(map[string]errorCodeEntry, len(r.codes))
	for k, v := range r.codes {
		out[k] = v
	}
	return out
}
