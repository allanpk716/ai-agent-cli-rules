package agentsdk

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	wqlogger "github.com/WQGroup/logger"
	"github.com/sirupsen/logrus"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// LoggerSettings configures a Logger instance. Each Logger created via NewLogger
// is fully independent — no global state is shared.
type LoggerSettings struct {
	// Level controls the minimum log level. Default: logrus.InfoLevel.
	Level logrus.Level
	// LogDir is the directory where log files are written.
	LogDir string
	// LogNameBase is the base name for log files (without extension). Default: appName.
	LogNameBase string
	// RotationTime controls how often time-based rotation occurs. Default: 24h.
	RotationTime time.Duration
	// MaxAgeDays is the maximum number of days to retain old log files. Default: 7.
	MaxAgeDays int
	// MaxSizeMB enables size-based rotation via lumberjack when > 0.
	// When 0 (default), time-based rotation via file-rotatelogs is used.
	MaxSizeMB int
	// UseHierarchicalPath uses a YYYY/MM/DD directory structure for log files.
	UseHierarchicalPath bool
}

// DefaultLoggerSettings returns a LoggerSettings with sensible defaults
// for the given application name and log directory.
func DefaultLoggerSettings(appName, logDir string) LoggerSettings {
	return LoggerSettings{
		Level:               logrus.InfoLevel,
		LogDir:              logDir,
		LogNameBase:         appName,
		RotationTime:        24 * time.Hour,
		MaxAgeDays:          7,
		MaxSizeMB:           0,
		UseHierarchicalPath: false,
	}
}

// Logger is an independent logging instance that wraps logrus with file
// rotation. Each Logger has its own logrus.Logger, file writer, and settings.
// There is no global state — multiple Loggers can coexist without interference.
type Logger struct {
	logger     *logrus.Logger
	fileWriter io.Closer
	settings   LoggerSettings
	stderrOnly bool
}

// NewLogger creates a new independent Logger from the provided settings.
// If the log directory cannot be created, the Logger degrades gracefully to
// stderr-only output (no error is returned).
func NewLogger(settings LoggerSettings) (*Logger, error) {
	l := &Logger{
		settings: settings,
	}

	// Attempt to create the log directory.
	if err := os.MkdirAll(settings.LogDir, 0755); err != nil {
		l.stderrOnly = true
		fmt.Fprintf(os.Stderr, "logger: failed to create log dir %q: %v — falling back to stderr only\n", settings.LogDir, err)
	}

	// Create an independent logrus.Logger instance.
	l.logger = logrus.New()
	l.logger.SetLevel(settings.Level)
	l.logger.Formatter = &wqlogger.WithFieldFormatter{
		TimestampFormat: "2006-01-02 15:04:05.000",
		DisableCaller:   true,
	}

	if !l.stderrOnly {
		var fileWriter io.Writer
		var closer io.Closer

		if settings.MaxSizeMB > 0 {
			// Size-based rotation via lumberjack.
			logDir := settings.LogDir
			if settings.UseHierarchicalPath {
				now := time.Now()
				logDir = filepath.Join(logDir, now.Format("2006"), now.Format("01"), now.Format("02"))
			}
			if err := os.MkdirAll(logDir, 0755); err != nil {
				l.stderrOnly = true
				fmt.Fprintf(os.Stderr, "logger: failed to create hierarchical log dir %q: %v — falling back to stderr only\n", logDir, err)
			} else {
				lj := &lumberjack.Logger{
					Filename:  filepath.Join(logDir, settings.LogNameBase+".log"),
					MaxSize:   settings.MaxSizeMB,
					MaxAge:    settings.MaxAgeDays,
					LocalTime: true,
					Compress:  false,
				}
				fileWriter = lj
				closer = lj
			}
		} else {
			// Time-based rotation via file-rotatelogs.
			var logPattern string
			if settings.UseHierarchicalPath {
				logPattern = filepath.Join(settings.LogDir, "%Y", "%m", "%d", settings.LogNameBase+"--%H%M--.log")
			} else {
				logPattern = filepath.Join(settings.LogDir, settings.LogNameBase+"--%Y%m%d%H%M--.log")
			}

			rl, err := rotatelogs.New(
				logPattern,
				rotatelogs.WithMaxAge(time.Duration(settings.MaxAgeDays)*24*time.Hour),
				rotatelogs.WithRotationTime(settings.RotationTime),
			)
			if err != nil {
				l.stderrOnly = true
				fmt.Fprintf(os.Stderr, "logger: failed to create rotate-logs writer: %v — falling back to stderr only\n", err)
			} else {
				fileWriter = rl
				closer = rl
			}
		}

		if !l.stderrOnly {
			l.fileWriter = closer
			l.logger.SetOutput(io.MultiWriter(os.Stderr, fileWriter))
		}
	}

	if l.stderrOnly {
		l.logger.SetOutput(os.Stderr)
	}

	// Best-effort cleanup of expired logs — ignore errors.
	_ = wqlogger.CleanupExpiredLogs(settings.LogDir, settings.MaxAgeDays)

	return l, nil
}

// Debug logs a message at Debug level.
func (l *Logger) Debug(args ...interface{}) {
	l.logger.Debug(args...)
}

// Info logs a message at Info level.
func (l *Logger) Info(args ...interface{}) {
	l.logger.Info(args...)
}

// Warn logs a message at Warn level.
func (l *Logger) Warn(args ...interface{}) {
	l.logger.Warn(args...)
}

// Error logs a message at Error level.
func (l *Logger) Error(args ...interface{}) {
	l.logger.Error(args...)
}

// WithField returns a logrus.Entry with a single attached field.
func (l *Logger) WithField(key string, value interface{}) *logrus.Entry {
	return l.logger.WithField(key, value)
}

// WithFields returns a logrus.Entry with multiple attached fields.
func (l *Logger) WithFields(fields map[string]interface{}) *logrus.Entry {
	return l.logger.WithFields(logrus.Fields(fields))
}

// Close releases the file writer resources. It returns any close error
// but never panics. Calling Close on an already-closed or stderr-only
// Logger is a safe no-op.
func (l *Logger) Close() error {
	if l.fileWriter == nil {
		return nil
	}
	err := l.fileWriter.Close()
	l.fileWriter = nil
	return err
}
