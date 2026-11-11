package logging

import "go.uber.org/zap"

// LoggerRef holds a reference to the current CLI logger. The logger is created
// with default (nop) at Wire init and replaced with the real logger in
// RootCommand.PersistentPreRun after Cobra parses flags, so logging respects
// --verbose, --quiet, and --json.
type LoggerRef struct {
	l *zap.Logger
}

// NewLoggerRef returns a LoggerRef with a nop logger. The root command must
// call Set with the result of ProvideLogger(flags) in PersistentPreRun so
// the logger is configured from parsed CLI flags.
func NewLoggerRef() *LoggerRef {
	return &LoggerRef{l: zap.NewNop()}
}

// Logger returns the current logger. Safe to call from any goroutine after
// the root command's PersistentPreRun has run.
func (r *LoggerRef) Logger() *zap.Logger {
	if r.l == nil {
		return zap.NewNop()
	}
	return r.l
}

// Set replaces the logger with one built from the given flags (e.g. from
// ProvideLogger). Call this from RootCommand.PersistentPreRun after flags
// are parsed. Syncs the previous logger if present.
func (r *LoggerRef) Set(l *zap.Logger) {
	if r.l != nil {
		_ = r.l.Sync()
	}
	r.l = l
}
