package log

import (
	"io"
	"os"

	"github.com/Sirupsen/logrus"
)

var stdoutLogger = logrus.New()
var stderrLogger = logrus.New()

func init() {
	stdoutLogger.Out = os.Stdout
	stderrLogger.Out = os.Stderr
}

func SetOutput(out io.Writer) {
	stdoutLogger.Out = out
	stderrLogger.Out = out
}

// Info is wrapper for logrus.Info to print to stdout
func Info(args ...interface{}) {
	stdoutLogger.Info(args...)
}

// Debug is wrapper for logrus.Debug to print to stdout
func Debug(args ...interface{}) {
	stdoutLogger.Debug(args...)
}

// Error is wrapper for logrus.Error to print to stderr
func Error(args ...interface{}) {
	stderrLogger.Error(args...)
}

// Fatal is wrapper for logrus.Fatal to print to stderr
func Fatal(args ...interface{}) {
	stderrLogger.Fatal(args...)
}

// Warn is wrapper for logrus.Warn to print to stderr
func Warn(args ...interface{}) {
	stderrLogger.Warn(args...)
}

// Infof is wrapper for logrus.Infof to print to stdout
func Infof(format string, args ...interface{}) {
	stdoutLogger.Infof(format, args...)
}

// Debugf is wrapper for logrus.Debugf to print to stdout
func Debugf(format string, args ...interface{}) {
	stdoutLogger.Debugf(format, args...)
}

// Errorf is wrapper for logrus.Errorf to print to stderr
func Errorf(format string, args ...interface{}) {
	stderrLogger.Errorf(format, args...)
}

// Fatalf is wrapper for logrus.Fatalf to print to stderr
func Fatalf(format string, args ...interface{}) {
	stderrLogger.Fatalf(format, args...)
}

// Warnf is wrapper for logrus.Warnf to print to stderr
func Warnf(format string, args ...interface{}) {
	stderrLogger.Warnf(format, args...)
}

// ParseLevel takes a string level and returns the Logrus log level constant.
func ParseLevel(lvl string) (logrus.Level, error) {
	return logrus.ParseLevel(lvl)
}

// SetLevelString takes in the log level in string format
// some of valid values: error, info, debug ...
func SetLevelString(lvlStr string) error {
	level, err := ParseLevel(lvlStr)
	if err != nil {
		return err
	}
	SetLevel(level)
	return nil
}

// SetLevel sets the log level
func SetLevel(lvl logrus.Level) {
	stdoutLogger.Level = lvl
	stderrLogger.Level = lvl
}

// GetLevel gets the current log level
func GetLevel() logrus.Level {
	return stdoutLogger.Level
}

// GetLevelString gets the current log level
func GetLevelString() string {
	var level string
	switch stdoutLogger.Level {
	case logrus.DebugLevel:
		level = "debug"
	case logrus.InfoLevel:
		level = "info"
	case logrus.ErrorLevel:
		level = "error"
	}

	return level
}
