package output

import (
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
)

type RootFormatter struct {
	humanFormatter *HumanFormatter
	jsonFormatter  *JSONFormatter
	logger         *logging.LoggerRef
	writer         Writer
	flags          *flags.GlobalFlags
}

func (f *RootFormatter) Print(data interface{}) error {
	if f.flags.QuietMode {
		return nil
	}
	if f.flags.OutputInJson {
		return f.jsonFormatter.Print(data)
	}
	return f.humanFormatter.Print(data)
}

func (f *RootFormatter) PrintError(err error) error {
	if f.flags.OutputInJson {
		return f.jsonFormatter.PrintError(err)
	}
	return f.humanFormatter.PrintError(err)
}

func (f *RootFormatter) IsJSON() bool {
	return f.flags.OutputInJson
}

func (f *RootFormatter) IsQuiet() bool {
	return f.flags.QuietMode
}

func NewRootFormatter(logger *logging.LoggerRef, writer Writer, flags *flags.GlobalFlags) Formatter {
	hFormatter := NewHumanFormatter(writer, logger)
	jFormatter := NewJSONFormatter(writer, logger)
	return &RootFormatter{
		humanFormatter: hFormatter,
		jsonFormatter:  jFormatter,
		logger:         logger,
		writer:         writer,
		flags:          flags,
	}
}
