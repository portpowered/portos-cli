package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap"
)

// ClientImpl implements the API client interface
type ClientImpl struct {
	configProvider *config.ManagerProvider
	httpClient     *http.Client
	authService    auth.Service
	logger         *logging.LoggerRef
	endpoints      *EndpointsClientImpl
	messages       *MessagesClientImpl
	rules          *RulesClientImpl
	relay          *RelayClientImpl
	websocket      *WebSocketClientImpl

	initOnce sync.Once
	baseURL  string
	initErr  error
}

// NewClient creates a new API client
// If httpClient is nil, a default client with 30s timeout will be created
func NewClient(
	configProvider *config.ManagerProvider,
	authService auth.Service,
	logger *logging.LoggerRef,
	httpClient *http.Client,
) Client {
	client := &ClientImpl{
		configProvider: configProvider,
		httpClient:     httpClient,
		authService:    authService,
		logger:         logger,
	}

	client.endpoints = &EndpointsClientImpl{
		client: client,
	}

	client.messages = &MessagesClientImpl{
		client: client,
	}

	client.rules = &RulesClientImpl{
		client: client,
	}

	client.websocket = &WebSocketClientImpl{
		client: client,
	}

	client.relay = &RelayClientImpl{
		client: client,
	}

	return client
}

func (c *ClientImpl) ensureInited() error {
	c.initOnce.Do(func() {
		mgr, err := c.configProvider.GetManager()
		if err != nil {
			c.initErr = err
			return
		}
		cfg, err := mgr.LoadConfig()
		if err != nil {
			c.initErr = fmt.Errorf("failed to load config: %w", err)
			return
		}
		c.baseURL = strings.TrimSuffix(cfg.APIBaseURL, "/")
	})
	return c.initErr
}

// Endpoints returns the endpoints API client
func (c *ClientImpl) Endpoints() EndpointsClient {
	return c.endpoints
}

// Messages returns the messages API client
func (c *ClientImpl) Messages() MessagesClient {
	return c.messages
}

// Relay returns the media relay API client
func (c *ClientImpl) Relay() RelayClient {
	return c.relay
}

// Rules returns the access control rules API client
func (c *ClientImpl) Rules() RulesClient {
	return c.rules
}

// RulesFor returns a rules client bound to baseURL for this invocation. The configured API base URL
// is never read, so an operator command can target a named environment without touching config.
func (c *ClientImpl) RulesFor(baseURL string) RulesClient {
	scoped := &ClientImpl{
		configProvider: c.configProvider,
		httpClient:     c.httpClient,
		authService:    c.authService,
		logger:         c.logger,
		baseURL:        strings.TrimSuffix(baseURL, "/"),
	}
	scoped.initOnce.Do(func() {})
	scoped.rules = &RulesClientImpl{client: scoped}
	return scoped.rules
}

// WebSocket returns the WebSocket API client
func (c *ClientImpl) WebSocket() WebSocketClient {
	return c.websocket
}

// doRequest performs an HTTP request with automatic Bearer token authentication and refresh
func (c *ClientImpl) doRequest(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	if err := c.ensureInited(); err != nil {
		return nil, &SetupError{Message: "failed to load config: " + err.Error(), Err: err}
	}
	// Get access token (will automatically refresh if needed)
	token, err := c.authService.GetAccessToken(ctx)
	if err != nil {
		return nil, NewSetupErrorWrapping(err)
	}

	if token == "" {
		return nil, &SetupError{Message: "no access token — authenticate with \"port auth login\", \"port auth set-token <token>\", or PORTOS_ACCESS_TOKEN", Err: auth.ErrNoCredentials}
	}

	// Build full URL — ensure path starts with "/" to avoid malformed URLs
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := c.baseURL + path

	// When body is present, read it so we can log the payload and then re-use it for the request
	var bodyReader io.Reader = body
	var payload []byte
	if body != nil {
		var err error
		payload, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set default headers
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Add custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Perform request — redact sensitive headers to avoid token leaks in verbose output
	safeReqHeaders := logging.RedactSensitiveHeaders(req.Header)
	if len(payload) > 0 {
		c.logger.Logger().Debug("Request", zap.String("method", method), zap.String("path", path), zap.Any("headers", safeReqHeaders), zap.ByteString("body", payload))
	} else {
		c.logger.Logger().Debug("Request", zap.String("method", method), zap.String("path", path), zap.Any("headers", safeReqHeaders))
	}
	resp, err := c.httpClient.Do(req)

	if err != nil {
		return nil, wrapConnectionError(err)
	}
	safeRespHeaders := logging.RedactSensitiveHeaders(resp.Header)
	c.logger.Logger().Debug("Response", zap.Int("status", resp.StatusCode), zap.Any("headers", safeRespHeaders))

	return resp, nil
}

// connectionSubstrings maps net/http error substrings to sentinel errors.
// This is the single place where fragile string inspection of stdlib errors
// happens — callers use errors.Is() against the sentinels instead.
var connectionSubstrings = []struct {
	substr   string
	sentinel error
}{
	{"unsupported protocol scheme", ErrInvalidURL},
	{"dial tcp", ErrConnectionFailed},
	{"connection refused", ErrConnectionFailed},
	{"no such host", ErrConnectionFailed},
	{"no route to host", ErrConnectionFailed},
	{"network unreachable", ErrConnectionFailed},
}

// wrapConnectionError inspects an error from httpClient.Do() and wraps it
// with the appropriate sentinel (ErrConnectionFailed or ErrInvalidURL) if
// it matches a known connection/protocol failure pattern. Unrecognised errors
// are wrapped with a generic "request failed" message.
func wrapConnectionError(err error) error {
	msg := strings.ToLower(err.Error())
	for _, cs := range connectionSubstrings {
		if strings.Contains(msg, strings.ToLower(cs.substr)) {
			return NewConnectionError(err.Error(), cs.sentinel)
		}
	}
	return fmt.Errorf("request failed: %w", err)
}

// handleResponse handles HTTP response, parsing JSON or error responses
func (c *ClientImpl) handleResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for error status codes
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Try to parse as API error
		var apiError APIError

		if err := json.Unmarshal(body, &apiError); err == nil {
			// Successfully parsed as API error
			errorMsg := "API error"
			if apiError.Message != nil {
				errorMsg = *apiError.Message
			} else if apiError.Code != nil {
				errorMsg = *apiError.Code
			}

			return &APIError{
				StatusCode: resp.StatusCode,
				Code:       apiError.Code,
				Family:     apiError.Family,
				Message:    apiError.Message,
				Err:        fmt.Errorf("%s (status: %d)", errorMsg, resp.StatusCode),
			}
		}

		// Not a JSON error, return generic error
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse successful response
	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}
