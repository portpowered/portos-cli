package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

// OperatorAuthCommand signs operators in with a client assertion or a Google service account.
type OperatorAuthCommand struct {
	login          auth.OperatorLogin
	configProvider *config.ManagerProvider
	formatter      output.Formatter
	logger         *logging.LoggerRef
}

// NewOperatorAuthCommand creates the operator login command.
func NewOperatorAuthCommand(login auth.OperatorLogin, configProvider *config.ManagerProvider, formatter output.Formatter, logger *logging.LoggerRef) *OperatorAuthCommand {
	return &OperatorAuthCommand{login: login, configProvider: configProvider, formatter: formatter, logger: logger}
}

// OperatorLoginParams are the operator login flags. Exactly one of ClientCredentials or GCPImpersonate is used.
type OperatorLoginParams struct {
	ClientCredentials bool
	ClientID          string
	KeyFile           string
	Alg               string
	KeyID             string
	GCPImpersonate    string
	Audience          string
	Environment       string
}

// IsOperator reports whether any operator login flag was used.
func (p OperatorLoginParams) IsOperator() bool {
	return p.ClientCredentials || p.GCPImpersonate != ""
}

// endpoint resolves the auth origin (token endpoint host and assertion audience) and the default Google audience from the environment.
func (c *OperatorAuthCommand) endpoint(environment string) (baseURL, audience string, err error) {
	mgr, err := c.configProvider.GetManager()
	if err != nil {
		return "", "", err
	}
	env, err := mgr.LoadEnvironment(environment)
	if err != nil {
		return "", "", err
	}
	return env.AuthURL, env.OperationsAudience, nil
}

func (p OperatorLoginParams) validate() error {
	if strings.TrimSpace(p.Environment) == "" {
		return fmt.Errorf("--environment is required with --client-credentials and --gcp-impersonate")
	}
	if p.ClientCredentials && p.GCPImpersonate != "" {
		return fmt.Errorf("--client-credentials and --gcp-impersonate cannot be combined")
	}
	if p.ClientCredentials {
		if strings.TrimSpace(p.ClientID) == "" || strings.TrimSpace(p.KeyFile) == "" {
			return fmt.Errorf("--client-id and --key-file are required with --client-credentials")
		}
	}
	return nil
}

// Login runs the selected operator login and reports success without printing any token.
func (c *OperatorAuthCommand) Login(ctx context.Context, p OperatorLoginParams) error {
	if err := p.validate(); err != nil {
		return err
	}
	baseURL, defaultAudience, err := c.endpoint(p.Environment)
	if err != nil {
		return err
	}
	if p.ClientCredentials {
		err = c.login.ClientCredentials(ctx, auth.ClientCredentialsParams{
			AuthURL: baseURL, ClientID: p.ClientID, KeyFile: p.KeyFile, Alg: p.Alg, KeyID: p.KeyID,
		})
	} else {
		audience := p.Audience
		if audience == "" {
			audience = defaultAudience
		}
		err = c.login.GCPImpersonate(ctx, auth.GCPParams{AuthURL: baseURL, ServiceAccount: p.GCPImpersonate, Audience: audience})
	}
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	c.logger.Logger().Info("operator login successful")
	return c.formatter.Print(SetTokenResult{Success: true, Message: "Login successful"})
}
