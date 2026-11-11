package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

// EndpointCommand handles endpoint-related commands
type EndpointCommand struct {
	apiClient      apiclient.Client
	formatter      output.Formatter
	logger         *logging.LoggerRef
	configProvider *config.ManagerProvider
	// stderr receives diagnostics that must not mix into the stdout document.
	stderr io.Writer
}

// NewEndpointCommand creates a new endpoint command
func NewEndpointCommand(apiClient apiclient.Client, formatter output.Formatter, logger *logging.LoggerRef, configProvider *config.ManagerProvider) *EndpointCommand {
	return &EndpointCommand{
		apiClient:      apiClient,
		formatter:      formatter,
		logger:         logger,
		configProvider: configProvider,
		stderr:         os.Stderr,
	}
}

// ListResult represents the result of the list command
type ListResult struct {
	Endpoints []EndpointDisplay `json:"endpoints"`
	NextToken *string           `json:"next_token,omitempty"`
}

// EndpointDisplay represents an endpoint for display purposes
type EndpointDisplay struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	Interfaces     []InterfaceDisplay     `json:"interfaces"`
	Manufacturer   *string                `json:"manufacturer,omitempty"`
	Model          *string                `json:"model,omitempty"`
	SerialNumber   *string                `json:"serial_number,omitempty"`
	Description    *string                `json:"description,omitempty"`
	ResourceGroups []ResourceGroupDisplay `json:"resource_groups,omitempty"`
	Routes         []RouteDisplay         `json:"routes,omitempty"`
	State          map[string]interface{} `json:"state"`
}

// InterfaceDisplay represents an interface for display purposes
type InterfaceDisplay struct {
	Name       string   `json:"name"`
	Version    string   `json:"version,omitempty"`
	Messages   []string `json:"commands,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// ResourceGroupDisplay represents a resource group for display purposes
type ResourceGroupDisplay struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RouteDisplay represents a route for display purposes
type RouteDisplay struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListParams contains parameters for the List command
type ListParams struct {
	Owner        string
	MaxResults   *int
	NextToken    *string
	Type         *string
	SerialNumber *string
}

// ShowParams contains parameters for the Show command
type ShowParams struct {
	EndpointID string
	Owner      string
}

// List runs the endpoint list command
func (c *EndpointCommand) List(ctx context.Context, params ListParams) error {
	// Use default owner if not provided
	owner := params.Owner
	if owner == "" {
		owner = "~caller"
	}

	// Call API
	response, err := c.apiClient.Endpoints().Enumerate(ctx, owner, params.NextToken, params.MaxResults, params.Type, params.SerialNumber)
	if err != nil {
		// Translate 400 errors when a next-token was provided into a user-friendly message
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && params.NextToken != nil {
			return fmt.Errorf("pagination token is invalid or expired — start a new paginated request without --next-token")
		}
		return fmt.Errorf("failed to enumerate endpoints: %w", err)
	}

	// Transform endpoints for display
	endpoints := make([]EndpointDisplay, len(response.Results))
	for i, ep := range response.Results {
		endpoints[i] = c.transformEndpoint(ep)
	}

	// Extract next token from pagination context
	var nextTokenStr *string
	if response.PaginationContext != nil && response.PaginationContext.NextToken != nil {
		nextTokenStr = response.PaginationContext.NextToken
	}

	result := ListResult{
		Endpoints: endpoints,
		NextToken: nextTokenStr,
	}

	// Output result
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}

	// Human-readable output
	return c.printListHumanReadable(result)
}

// Show runs the endpoint show command
func (c *EndpointCommand) Show(ctx context.Context, params ShowParams) error {
	endpointID := params.EndpointID

	// Query the API directly by endpoint ID
	response, err := c.apiClient.Endpoints().GetByIDWithExpand(ctx, endpointID, "interfaces.attributes", false)
	if err != nil {
		return fmt.Errorf("failed to get endpoint: %w", err)
	}

	if len(response.Results) == 0 {
		return fmt.Errorf("endpoint not found: %s", params.EndpointID)
	}

	// Transform endpoint for display
	endpoint := c.transformEndpoint(response.Results[0])
	endpoint.State = stateFromAttributes(response.Results[0].Attributes)

	// Output result
	if c.formatter.IsJSON() {
		return c.formatter.Print(endpoint)
	}

	// Human-readable output
	return c.formatter.Print(c.printShowHumanReadable(endpoint))
}

func stateFromAttributes(attributes *[]api.AttributeState) map[string]interface{} {
	state := make(map[string]interface{})
	if attributes == nil {
		return state
	}

	for _, attr := range *attributes {
		key := attr.Name
		if key == "" || attr.Value == nil {
			continue
		}

		value := any(*attr.Value)
		if rawValue, ok := (*attr.Value)["value"]; ok {
			value = rawValue
		}
		if _, exists := state[key]; exists {
			key = fmt.Sprintf("%s/%s", attr.Namespace, attr.Name)
		}
		state[key] = value
	}

	return state
}

// transformEndpoint converts an API endpoint to a display endpoint
func (c *EndpointCommand) transformEndpoint(ep api.Endpoint) EndpointDisplay {
	// Transform type from API format to display format
	displayType := transformEndpointType(ep.Type)

	display := EndpointDisplay{
		ID:         ep.Id,
		Name:       ep.Name.Value,
		Type:       displayType,
		Interfaces: make([]InterfaceDisplay, len(ep.Interfaces)),
	}

	// Transform interfaces
	for i, iface := range ep.Interfaces {
		// Strip prefix from interface name (e.g., "port1/systems/zero/capability-interfaces/power" -> "power")
		interfaceName := iface.Name
		if lastSlash := strings.LastIndex(interfaceName, "/"); lastSlash >= 0 {
			interfaceName = interfaceName[lastSlash+1:]
		}

		display.Interfaces[i] = InterfaceDisplay{
			Name:       interfaceName,
			Messages:   extractMessageNames(iface.Messages),
			Attributes: extractAttributeNames(iface.Attributes),
		}
	}

	// Transform optional fields
	if ep.Manufacturer != nil {
		display.Manufacturer = &ep.Manufacturer.Value
	}
	if ep.Model != nil {
		display.Model = &ep.Model.Value
	}
	if ep.SerialNumber != nil {
		display.SerialNumber = &ep.SerialNumber.Value
	}
	if ep.Description != nil {
		display.Description = &ep.Description.Value
	}

	// Transform resource groups
	if ep.ResourceGroups != nil {
		display.ResourceGroups = make([]ResourceGroupDisplay, len(*ep.ResourceGroups))
		for i, rg := range *ep.ResourceGroups {
			display.ResourceGroups[i] = ResourceGroupDisplay{
				ID:   rg.Id,
				Name: rg.Name,
			}
		}
	}

	// Transform routes
	if ep.Routes != nil {
		display.Routes = make([]RouteDisplay, len(*ep.Routes))
		for i, route := range *ep.Routes {
			routeName := ""
			if route.Name != nil {
				routeName = route.Name.Value
			}
			display.Routes[i] = RouteDisplay{
				ID:   route.Id,
				Name: routeName,
			}
		}
	}

	return display
}

// extractCommandNames extracts command names from interface commands
func extractMessageNames(messages *[]api.ApplicationInterfaceMessage) []string {
	if messages == nil {
		return nil
	}
	names := make([]string, len(*messages))
	for i, msg := range *messages {
		names[i] = msg.Name
	}
	return names
}

// extractAttributeNames extracts attribute names from interface attributes
func extractAttributeNames(attributes *[]api.ApplicationInterfaceAttribute) []string {
	if attributes == nil {
		return nil
	}
	names := make([]string, len(*attributes))
	for i, attr := range *attributes {
		names[i] = attr.Name
	}
	return names
}

// transformEndpointType converts API endpoint type to display format
func transformEndpointType(apiType string) string {
	// Map API types to display types
	switch apiType {
	case "LIGHT":
		return "smart_light"
	default:
		// For other types, convert to lowercase with underscores if needed
		// For now, return as-is if no specific mapping exists
		return apiType
	}
}

// printListHumanReadable prints the list result in human-readable format
func (c *EndpointCommand) printListHumanReadable(result ListResult) error {
	var sb strings.Builder

	if len(result.Endpoints) == 0 {
		sb.WriteString("No endpoints found.\n")
		c.printServerHint()
		return c.formatter.Print(sb.String())
	}

	sb.WriteString("Endpoints:\n")
	sb.WriteString("\n")

	for _, ep := range result.Endpoints {
		sb.WriteString(fmt.Sprintf("ID: %s\n", ep.ID))
		sb.WriteString(fmt.Sprintf("Name: %s\n", ep.Name))
		sb.WriteString(fmt.Sprintf("Type: %s\n", ep.Type))

		if ep.Manufacturer != nil {
			sb.WriteString(fmt.Sprintf("Manufacturer: %s\n", *ep.Manufacturer))
		}
		if ep.Model != nil {
			sb.WriteString(fmt.Sprintf("Model: %s\n", *ep.Model))
		}
		if ep.SerialNumber != nil {
			sb.WriteString(fmt.Sprintf("Serial Number: %s\n", *ep.SerialNumber))
		}
		if ep.Description != nil {
			sb.WriteString(fmt.Sprintf("Description: %s\n", *ep.Description))
		}

		if len(ep.Interfaces) > 0 {
			sb.WriteString("Interfaces:\n")
			for _, iface := range ep.Interfaces {
				if iface.Version != "" {
					sb.WriteString(fmt.Sprintf("  - %s (v%s)\n", iface.Name, iface.Version))
				} else {
					sb.WriteString(fmt.Sprintf("  - %s\n", iface.Name))
				}
				if len(iface.Messages) > 0 {
					sb.WriteString(fmt.Sprintf("    Messages: %s\n", strings.Join(iface.Messages, ", ")))
				}
				if len(iface.Attributes) > 0 {
					sb.WriteString(fmt.Sprintf("    Attributes: %s\n", strings.Join(iface.Attributes, ", ")))
				}
			}
		}

		if len(ep.ResourceGroups) > 0 {
			sb.WriteString("Resource Groups:\n")
			for _, rg := range ep.ResourceGroups {
				sb.WriteString(fmt.Sprintf("  - %s (%s)\n", rg.Name, rg.ID))
			}
		}

		if len(ep.Routes) > 0 {
			sb.WriteString("Routes:\n")
			for _, route := range ep.Routes {
				sb.WriteString(fmt.Sprintf("  - %s (%s)\n", route.Name, route.ID))
			}
		}

		sb.WriteString("\n")
	}

	if result.NextToken != nil {
		sb.WriteString(fmt.Sprintf("Next token: %s\n", *result.NextToken))
		sb.WriteString("(Use --next-token to fetch more results)\n")
	}

	return c.formatter.Print(sb.String())
}

// printShowHumanReadable prints the show result in human-readable format
func (c *EndpointCommand) printShowHumanReadable(endpoint EndpointDisplay) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Endpoint: %s\n", endpoint.ID))
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("Name: %s\n", endpoint.Name))
	sb.WriteString(fmt.Sprintf("Type: %s\n", endpoint.Type))

	if endpoint.Manufacturer != nil {
		sb.WriteString(fmt.Sprintf("Manufacturer: %s\n", *endpoint.Manufacturer))
	}
	if endpoint.Model != nil {
		sb.WriteString(fmt.Sprintf("Model: %s\n", *endpoint.Model))
	}
	if endpoint.SerialNumber != nil {
		sb.WriteString(fmt.Sprintf("Serial Number: %s\n", *endpoint.SerialNumber))
	}
	if endpoint.Description != nil {
		sb.WriteString(fmt.Sprintf("Description: %s\n", *endpoint.Description))
	}

	writeShowInterfaces(&sb, endpoint.Interfaces)

	if len(endpoint.ResourceGroups) > 0 {
		sb.WriteString("Resource Groups:\n")
		for _, rg := range endpoint.ResourceGroups {
			sb.WriteString(fmt.Sprintf("  - %s (%s)\n", rg.Name, rg.ID))
		}
		sb.WriteString("\n")
	}

	if len(endpoint.Routes) > 0 {
		sb.WriteString("Routes:\n")
		for _, route := range endpoint.Routes {
			sb.WriteString(fmt.Sprintf("  - %s (%s)\n", route.Name, route.ID))
		}
		sb.WriteString("\n")
	}

	writeShowState(&sb, endpoint.State)
	sb.WriteString("\n")

	return sb.String()
}

// writeShowInterfaces writes the interfaces section of the human-readable show output.
func writeShowInterfaces(sb *strings.Builder, interfaces []InterfaceDisplay) {
	if len(interfaces) > 0 {
		sb.WriteString("\n")
		sb.WriteString("Interfaces:\n")
		for _, iface := range interfaces {
			if iface.Version != "" {
				sb.WriteString(fmt.Sprintf("  %s (v%s)\n", iface.Name, iface.Version))
			} else {
				sb.WriteString(fmt.Sprintf("  %s\n", iface.Name))
			}
			if len(iface.Messages) > 0 {
				sb.WriteString("    Messages:\n")
				for _, msg := range iface.Messages {
					sb.WriteString(fmt.Sprintf("      - %s\n", msg))
				}
			}
			if len(iface.Attributes) > 0 {
				sb.WriteString("    Attributes:\n")
				for _, attr := range iface.Attributes {
					sb.WriteString(fmt.Sprintf("      - %s\n", attr))
				}
			}
			sb.WriteString("\n")
		}
	}

}

// writeShowState writes the state section of the human-readable show output.
func writeShowState(sb *strings.Builder, state map[string]interface{}) {
	sb.WriteString("State:\n")
	if len(state) == 0 {
		sb.WriteString("  No state available\n")
	} else {
		keys := make([]string, 0, len(state))
		for k := range state {
			keys = append(keys, k)
		}
		// Sort keys for deterministic output
		for i := 0; i < len(keys); i++ {
			for j := i + 1; j < len(keys); j++ {
				if keys[i] > keys[j] {
					keys[i], keys[j] = keys[j], keys[i]
				}
			}
		}
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("  %s: %v\n", k, state[k]))
		}
	}
}
