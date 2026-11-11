package flags

type GlobalFlags struct {
	OutputInJson  bool   // Output the results in JSON format rather the default format.
	QuietMode     bool   // Suppress all output
	VerboseMode   bool   // Enable verbose/debug output
	ConfigDirPath string // Override directory for Port OS CLI config (default: ~/.portos)
	Lease         string // Hold lease for messages to a held endpoint (default: $PORTOS_LEASE)
}

func NewGlobalFlags() *GlobalFlags {
	return &GlobalFlags{
		OutputInJson:  false,
		QuietMode:     false,
		VerboseMode:   false,
		ConfigDirPath: "",
	}
}

// ConfigDir returns the config directory override (empty means use default ~/.portos).
// Used by config.ManagerProvider to resolve the config directory after flags are parsed.
func (f *GlobalFlags) ConfigDir() string {
	return f.ConfigDirPath
}
