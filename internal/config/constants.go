package config

// Default API base URL
const DefaultAPIBaseURL = "https://api.portoperatingsystem.com"

// Default Auth base URL (where OAuth/auth endpoints live)
const DefaultAuthBaseURL = "https://auth.portoperatingsystem.com"

// Configuration file names
const (
	ConfigFileName      = "config.yaml"
	CredentialsFileName = "credentials.yaml"
)

// Directory name for Port OS CLI configuration
const ConfigDirName = ".portos"

// Environment variable prefix
const EnvPrefixPORTOS = "PORTOS_"

// Environment variable names for configuration
const (
	EnvAPIBaseURL                  = "PORTOS_API_BASE_URL"
	EnvAuthBaseURL                 = "PORTOS_AUTH_BASE_URL"
	EnvBrowserAuthorizationBaseURL = "PORTOS_BROWSER_AUTHORIZATION_BASE_URL"
)

// Environment variable names for credentials
const (
	EnvAccessToken  = "PORTOS_ACCESS_TOKEN"
	EnvRefreshToken = "PORTOS_REFRESH_TOKEN"
	EnvExpiresAt    = "PORTOS_EXPIRES_AT"
	EnvTokenType    = "PORTOS_TOKEN_TYPE"
)
