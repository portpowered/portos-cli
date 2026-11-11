//go:build wireinject
// +build wireinject

//go:generate wire

package wire

import (
	"net/http"

	"github.com/google/wire"
	api "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/cli"
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/http_client"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
	"github.com/portpowered/portos-cli/test/mocks"
)

// ConfigSet provides configuration-related dependencies.
// ManagerProvider is created with a ConfigDirResolver (bound from *flags.GlobalFlags in wire.Build)
// so the config directory is resolved after CLI flags are parsed; the manager is created once on first use.
var ConfigSet = wire.NewSet(
	config.NewManagerProvider,
)

// LoggerSet provides logging-related dependencies. The logger is configured
// from CLI flags in RootCommand.PersistentPreRun after Cobra parses flags.
var LoggerSet = wire.NewSet(
	logging.NewLoggerRef,
)

// AuthSet provides authentication-related dependencies
var AuthSet = wire.NewSet(
	auth.NewTokenStorage,
	auth.NewDeviceAuthClient,
	auth.NewBrowserAuthClient,
	auth.NewService,
	auth.NewExecRunner,
	auth.NewOperatorLogin,
)

// FlagsSet provides global flags dependencies
var FlagsSet = wire.NewSet(
	flags.NewGlobalFlags,
)

var CommandsSet = wire.NewSet(
	commands.NewRecorder,
	commands.NewInitCommand,
	commands.NewStatusCommand,
	commands.NewAuthCommand,
	commands.NewEndpointCommand,
	commands.NewGroupCommand,
	commands.NewMessageCommand,
	commands.NewSnapshotCommand,
	commands.NewRecordCommand,
	commands.NewMoveCommand,
	commands.NewRulesCommand,
	commands.NewEstablishCommand,
	commands.NewOperatorAuthCommand,
	commands.NewServeCommand,
	commands.NewSessionCommand,
)

var CliSet = wire.NewSet(
	cli.NewRouter,
	cli.NewInitCommand,
	cli.NewStatusCommand,
	cli.NewAuthCommand,
	cli.NewAuthSetTokenCommand,
	cli.NewAuthLoginCommand,
	cli.NewEndpointCommand,
	cli.NewEndpointListCommand,
	cli.NewEndpointShowCommand,
	cli.NewEndpointAttributeCommand,
	cli.NewGroupCommand,
	cli.NewGroupListCommand,
	cli.NewMessageCommand,
	cli.NewSnapshotCommand,
	cli.NewRecordCommand,
	cli.NewMoveCliCommand,
	cli.NewListCommandsCliCommand,
	cli.NewRulesCommand,
	cli.NewListRulesCommand,
	cli.NewCreateRuleCommand,
	cli.NewDeleteRuleCommand,
	cli.NewEstablishGroupCommand,
	cli.NewEstablishGrantCommand,
	cli.NewEstablishRevokeCommand,
	cli.NewEstablishCheckCommand,
	cli.NewEstablishListCommand,
	cli.NewSessionGroupCommand,
	cli.NewSessionHoldCommand,
	cli.NewSessionStatusCommand,
	cli.NewSessionReleaseCommand,
	cli.NewServeCommand,
	cli.NewHostCommand,
	cli.NewRootCommand,
)

// TransportSet provides the resolver that picks cloud or session host per message.
var TransportSet = wire.NewSet(
	transport.NewResolver,
)

// OutputSet provides output formatting dependencies
var OutputSet = wire.NewSet(
	output.NewRootFormatter,
	output.NewHumanFormatter,
	output.NewJSONFormatter,
)

// APISet provides API client dependencies
var APISet = wire.NewSet(
	api.NewClient,
)

var HTTPClientSet = wire.NewSet(
	http_client.ProvideHTTPClient,
)

var MockHTTPClientSet = wire.NewSet(
	mocks.NewMockHTTPClient,
)

var CliWriterSet = wire.NewSet(
	output.NewCLIWriter,
)

// CLISet combines all dependency sets for CLI
var CLISet = wire.NewSet(
	cli.NewCLIApp,
	ConfigSet,
	LoggerSet,
	AuthSet,
	OutputSet,
	APISet,
	CliSet,
	FlagsSet,
	CommandsSet,
	TransportSet,
)

// InitializeCLI sets up dependencies for CLI using Wire
func InitializeCLI() (*cli.CLIApp, error) {
	wire.Build(
		HTTPClientSet,
		CliWriterSet,
		CLISet,
		wire.Bind(new(config.ConfigDirResolver), new(*flags.GlobalFlags)),
	)
	return &cli.CLIApp{}, nil
}

// Used to initialize the CLI but with test responses from the cloud
func InitializeMockCLI(httpClient *http.Client, cliWriter output.Writer) (*cli.CLIApp, error) {
	wire.Build(
		CLISet,
		wire.Bind(new(config.ConfigDirResolver), new(*flags.GlobalFlags)),
	)
	return &cli.CLIApp{}, nil
}
