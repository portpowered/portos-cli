package cli

import (
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/spf13/cobra"
)

// Router defines the routes and the wiring for all the routes.
type Router struct {
	LoggerRef *logging.LoggerRef
	Flags     *flags.GlobalFlags

	RootCommand *RootCommand

	InitCommand *InitCommand

	AuthCommand         *AuthCommand
	AuthSetTokenCommand *AuthSetTokenCommand
	AuthLoginCommand    *AuthLoginCommand

	StatusCommand *StatusCommand

	EndpointCommand          *EndpointCommand
	EndpointListCommand      *EndpointListCommand
	EndpointShowCommand      *EndpointShowCommand
	EndpointAttributeCommand *EndpointAttributeCommand

	GroupCommand     *GroupCommand
	GroupListCommand *GroupListCommand

	MessageCommand         *MessageCommand
	SnapshotCommand        *SnapshotCommand
	RecordCommand          *RecordCommand
	PlayAudioCliCommand    *PlayAudioCliCommand
	MoveCliCommand         *MoveCliCommand
	ListCommandsCliCommand *ListCommandsCliCommand

	RulesCliCommand   *RulesCommand
	ListRulesCommand  *ListRulesCommand
	CreateRuleCommand *CreateRuleCommand
	DeleteRuleCommand *DeleteRuleCommand

	EstablishGroupCommand  *EstablishGroupCommand
	EstablishGrantCommand  *EstablishGrantCommand
	EstablishRevokeCommand *EstablishRevokeCommand
	EstablishCheckCommand  *EstablishCheckCommand
	EstablishListCommand   *EstablishListCommand

	SessionGroupCommand   *SessionGroupCommand
	SessionHoldCommand    *SessionHoldCommand
	SessionStatusCommand  *SessionStatusCommand
	SessionReleaseCommand *SessionReleaseCommand

	ServeCommand *ServeCommand
	HostCommand  *HostCommand
}

// NewRootCommand constructs a RootCommand with the given dependencies.
func NewRouter(
	loggerRef *logging.LoggerRef,
	flags *flags.GlobalFlags,
	rootCommand *RootCommand,
	initCommand *InitCommand,
	statusCommand *StatusCommand,
	authCommand *AuthCommand,
	authSetTokenCommand *AuthSetTokenCommand,
	authLoginCommand *AuthLoginCommand,
	endpointCommand *EndpointCommand,
	endpointListCommand *EndpointListCommand,
	endpointShowCommand *EndpointShowCommand,
	endpointAttributeCommand *EndpointAttributeCommand,
	groupCommand *GroupCommand,
	groupListCommand *GroupListCommand,
	messageCommand *MessageCommand,
	snapshotCommand *SnapshotCommand,
	recordCommand *RecordCommand,
	playAudioCliCommand *PlayAudioCliCommand,
	moveCliCommand *MoveCliCommand,
	listCommandsCliCommand *ListCommandsCliCommand,
	rulesCliCommand *RulesCommand,
	listRulesCommand *ListRulesCommand,
	createRuleCommand *CreateRuleCommand,
	deleteRuleCommand *DeleteRuleCommand,
	establishGroupCommand *EstablishGroupCommand,
	establishGrantCommand *EstablishGrantCommand,
	establishRevokeCommand *EstablishRevokeCommand,
	establishCheckCommand *EstablishCheckCommand,
	establishListCommand *EstablishListCommand,
	sessionGroupCommand *SessionGroupCommand,
	sessionHoldCommand *SessionHoldCommand,
	sessionStatusCommand *SessionStatusCommand,
	sessionReleaseCommand *SessionReleaseCommand,
	serveCommand *ServeCommand,
	hostCommand *HostCommand,
) *Router {
	return &Router{
		LoggerRef:                loggerRef,
		Flags:                    flags,
		RootCommand:              rootCommand,
		InitCommand:              initCommand,
		StatusCommand:            statusCommand,
		AuthCommand:              authCommand,
		AuthSetTokenCommand:      authSetTokenCommand,
		AuthLoginCommand:         authLoginCommand,
		EndpointCommand:          endpointCommand,
		EndpointListCommand:      endpointListCommand,
		EndpointShowCommand:      endpointShowCommand,
		EndpointAttributeCommand: endpointAttributeCommand,
		GroupCommand:             groupCommand,
		GroupListCommand:         groupListCommand,
		MessageCommand:           messageCommand,
		SnapshotCommand:          snapshotCommand,
		RecordCommand:            recordCommand,
		PlayAudioCliCommand:      playAudioCliCommand,
		MoveCliCommand:           moveCliCommand,
		ListCommandsCliCommand:   listCommandsCliCommand,
		RulesCliCommand:          rulesCliCommand,
		ListRulesCommand:         listRulesCommand,
		CreateRuleCommand:        createRuleCommand,
		DeleteRuleCommand:        deleteRuleCommand,
		EstablishGroupCommand:    establishGroupCommand,
		EstablishGrantCommand:    establishGrantCommand,
		EstablishRevokeCommand:   establishRevokeCommand,
		EstablishCheckCommand:    establishCheckCommand,
		EstablishListCommand:     establishListCommand,
		SessionGroupCommand:      sessionGroupCommand,
		SessionHoldCommand:       sessionHoldCommand,
		SessionStatusCommand:     sessionStatusCommand,
		SessionReleaseCommand:    sessionReleaseCommand,
		ServeCommand:             serveCommand,
		HostCommand:              hostCommand,
	}
}

// Defines the overall routing structure of commands as part of the CLI application.
func (c Router) BuildRoot() *cobra.Command {
	root := NewPath("port", c.RootCommand.Generate())

	root.AddCommand(NewPath("init", c.InitCommand.Generate()))
	root.AddCommand(NewPath("status", c.StatusCommand.Generate()))
	root.AddCommand(NewPath("serve", c.ServeCommand.Generate()))
	root.AddCommand(NewPath("__host", c.HostCommand.Generate()))

	authGroup := NewPath("auth", c.AuthCommand.Generate())
	authGroup.AddCommand(NewPath("set-token", c.AuthSetTokenCommand.Generate()))
	authGroup.AddCommand(NewPath("login", c.AuthLoginCommand.Generate()))
	root.AddCommand(authGroup)

	endpointGroup := NewPath("endpoint", c.EndpointCommand.Generate())
	endpointGroup.AddCommand(NewPath("list", c.EndpointListCommand.Generate()))
	endpointGroup.AddCommand(NewPath("show <endpoint-id>", c.EndpointShowCommand.Generate()))
	endpointGroup.AddCommand(NewPath("attribute <endpoint-id> <interface>/<attribute>", c.EndpointAttributeCommand.Generate()))
	root.AddCommand(endpointGroup)

	groupGroup := NewPath("group", c.GroupCommand.Generate())
	groupGroup.AddCommand(NewPath("list", c.GroupListCommand.Generate()))
	root.AddCommand(groupGroup)

	msgGroup := NewPath("message", c.MessageCommand.Generate())
	msgGroup.AddCommand(NewPath("snapshot <endpoint-id> [output-path]", c.SnapshotCommand.Generate()))
	msgGroup.AddCommand(NewPath("record <endpoint-id>", c.RecordCommand.Generate()))
	msgGroup.AddCommand(NewPath("play-audio <endpoint-id>", c.PlayAudioCliCommand.Generate()))
	msgGroup.AddCommand(NewPath("move <endpoint-id> <instructions...>", c.MoveCliCommand.Generate()))
	msgGroup.AddCommand(NewPath("go-to-point <endpoint-id> <x> <y>", c.MoveCliCommand.GenerateGoToPoint()))
	msgGroup.AddCommand(NewPath("step-ptz <endpoint-id> <direction>", c.MoveCliCommand.GenerateStepPTZ()))
	msgGroup.AddCommand(NewPath("list-commands <endpoint-id>", c.ListCommandsCliCommand.Generate()))
	root.AddCommand(msgGroup)

	sessionGroup := NewPath("session", c.SessionGroupCommand.Generate())
	sessionGroup.AddCommand(NewPath("hold <endpoint-id>", c.SessionHoldCommand.Generate()))
	sessionGroup.AddCommand(NewPath("status [hold-id]", c.SessionStatusCommand.Generate()))
	sessionGroup.AddCommand(NewPath("release <hold-id>", c.SessionReleaseCommand.Generate()))
	root.AddCommand(sessionGroup)

	rulesGroup := NewPath("rules", c.RulesCliCommand.Generate())
	rulesGroup.AddCommand(NewPath("list", c.ListRulesCommand.Generate()))
	rulesGroup.AddCommand(NewPath("create", c.CreateRuleCommand.Generate()))
	rulesGroup.AddCommand(NewPath("delete <rule-id>", c.DeleteRuleCommand.Generate()))
	establishGroup := NewPath("establish", c.EstablishGroupCommand.Generate())
	establishGroup.AddCommand(NewPath("grant", c.EstablishGrantCommand.Generate()))
	establishGroup.AddCommand(NewPath("revoke", c.EstablishRevokeCommand.Generate()))
	establishGroup.AddCommand(NewPath("check", c.EstablishCheckCommand.Generate()))
	establishGroup.AddCommand(NewPath("list", c.EstablishListCommand.Generate()))
	rulesGroup.AddCommand(establishGroup)
	root.AddCommand(rulesGroup)

	cmd := root.CreateCommand()
	cmd.PersistentFlags().BoolVarP(&c.Flags.OutputInJson, "json", "j", false, "Output in JSON format")
	cmd.PersistentFlags().BoolVarP(&c.Flags.QuietMode, "quiet", "q", false, "Suppress all output")
	cmd.PersistentFlags().BoolVarP(&c.Flags.VerboseMode, "verbose", "v", false, "Enable verbose/debug output")
	cmd.PersistentFlags().StringVarP(&c.Flags.ConfigDirPath, "config-dir", "C", "", "Directory for Port OS CLI config (default: ~/.portos)")
	cmd.PersistentFlags().StringVar(&c.Flags.Lease, "lease", "", "Hold lease for messages to a held endpoint (default: $PORTOS_LEASE)")
	// Pre-register --version with -V (uppercase) shorthand. Cobra's default shorthand is -v,
	// which conflicts with --verbose. Cobra skips adding its own version flag when one already exists.
	cmd.Flags().BoolP("version", "V", false, "Print the version number")

	return cmd
}
