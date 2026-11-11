package logging

import (
	"github.com/portpowered/portos-cli/internal/flags"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ProvideLogger provides a logger for CLI with the following behavior:
// - Verbose mode: prints all logs including debug
// - Quiet mode or JSON flag: prints nothing (silent)
// - Default: only prints fatal logs
func ProvideLogger(flags *flags.GlobalFlags) *zap.Logger {
	if flags.VerboseMode {
		// Verbose mode: print all logs including debug
		logger, err := zap.NewDevelopment()
		if err != nil {
			panic("failed to initialize logger: " + err.Error())
		}
		return logger
	} else if flags.QuietMode || flags.OutputInJson {
		// Quiet mode or JSON flag: don't print anything
		return zap.NewNop()
	} else {
		// Default: only print fatal logs
		config := zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(zapcore.FatalLevel)
		logger, err := config.Build()
		if err != nil {
			panic("failed to initialize logger: " + err.Error())
		}
		return logger
	}
}
