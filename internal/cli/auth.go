package cli

import (
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// AuthCommand is the auth group (parent command); subcommands are wired in routes.go.
type AuthCommand struct{}

// NewAuthCommand returns the auth group command constructor.
func NewAuthCommand() *AuthCommand {
	return &AuthCommand{}
}

// Generate returns the cobra command for the auth group (no subcommands; wired in routes).
func (c *AuthCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Authentication commands",
		Long: `Manage authentication for the Port OS CLI.

Prerequisite: run 'port init' first to create a configuration directory.

Available authentication methods:

  login        OAuth 2.0 device authorization flow. Use when you have a
               client ID from the Port OS dashboard and a deployed
               authorization server.
  set-token    Set an access token directly. Use when you already have a
               token (e.g., from an API or another tool).

You can also set the PORTOS_ACCESS_TOKEN environment variable to authenticate
without storing credentials on disk.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
}

// AuthSetTokenCommand wraps the auth set-token subcommand.
type AuthSetTokenCommand struct {
	command *commands.AuthCommand
}

// NewAuthSetTokenCommand returns a new AuthSetTokenCommand.
func NewAuthSetTokenCommand(command *commands.AuthCommand) *AuthSetTokenCommand {
	return &AuthSetTokenCommand{command: command}
}

// Generate returns the cobra command for auth set-token.
func (c *AuthSetTokenCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Use:   "set-token [token]",
		Short: "Set authentication token directly",
		Long: `Set an access token directly without going through device authorization flow.

Note: tokens are stored in plaintext in ~/.portos/credentials.yaml.
For sensitive environments, use the PORTOS_ACCESS_TOKEN environment variable instead,
which avoids writing the token to disk.

  # TODO: OS keychain integration (macOS Keychain, Linux Secret Service) is a future enhancement.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			params := commands.SetTokenParams{Token: args[0]}
			return c.command.SetToken(ctx, params)
		},
	}
}

// AuthLoginCommand wraps the auth login subcommand.
type AuthLoginCommand struct {
	command  *commands.AuthCommand
	operator *commands.OperatorAuthCommand
}

// NewAuthLoginCommand returns a new AuthLoginCommand.
func NewAuthLoginCommand(command *commands.AuthCommand, operator *commands.OperatorAuthCommand) *AuthLoginCommand {
	return &AuthLoginCommand{command: command, operator: operator}
}

// Generate returns the cobra command for auth login.
func (c *AuthLoginCommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login using browser-based OAuth flow",
		Long: `Initiate browser-based OAuth 2.0 login to authenticate with Port OS.

A browser window will open to the Port OS authorization server. After you
sign in and grant access, the CLI will automatically receive a token.

If the browser does not open automatically, copy the URL shown in the
terminal and open it manually.

Use --device where no browser can open on this machine (a remote shell or
an agent sandbox): the CLI prints a code and a link, you approve it in any
signed-in browser, and the CLI stores a refreshable login.

You can also authenticate without a browser:

  port auth set-token <token>    Set an access token directly
  PORTOS_ACCESS_TOKEN=<token>    Set a token via environment variable

Operators can sign in without a browser:

  --client-credentials   Sign a client assertion (private_key_jwt) with --key-file
  --gcp-impersonate SA   Mint the service account's Google ID token and exchange it

Both store a short-lived operational token that has no refresh token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			params := operatorLoginParams(cmd)
			if params.IsOperator() {
				return c.operator.Login(ctx, params)
			}
			device, _ := cmd.Flags().GetBool("device")
			return c.command.Login(ctx, commands.LoginParams{Device: device})
		},
	}
	flags := cmd.Flags()
	flags.Bool("device", false, "Sign in with a device code approved in any signed-in browser, for machines that cannot open one")
	flags.Bool("client-credentials", false, "Sign in as an operator OAuth client with a signed client assertion (private_key_jwt)")
	flags.String("client-id", "", "OAuth client ID to assert; required with --client-credentials")
	flags.String("key-file", "", "Path to the PEM private key that signs the client assertion; required with --client-credentials")
	flags.String("alg", "", "Signing algorithm for the client assertion, RS256 or ES256 (default: RS256 for RSA keys, ES256 for EC keys)")
	flags.String("kid", "", "Key ID placed in the client assertion header")
	flags.String("gcp-impersonate", "", "Service account whose Google ID token to mint with your gcloud login (needs roles/iam.serviceAccountOpenIdTokenCreator on it)")
	flags.String("audience", "", "Google audience for the identity token (default: operationsAudience of --environment)")
	flags.String("environment", "", "Name of an entry in the CLI config environments map to sign in to")
	return cmd
}

func operatorLoginParams(cmd *cobra.Command) commands.OperatorLoginParams {
	flags := cmd.Flags()
	get := func(name string) string { v, _ := flags.GetString(name); return v }
	creds, _ := flags.GetBool("client-credentials")
	return commands.OperatorLoginParams{
		ClientCredentials: creds,
		ClientID:          get("client-id"),
		KeyFile:           get("key-file"),
		Alg:               get("alg"),
		KeyID:             get("kid"),
		GCPImpersonate:    get("gcp-impersonate"),
		Audience:          get("audience"),
		Environment:       get("environment"),
	}
}
