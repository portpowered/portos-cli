package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/output"
)

// GroupCommand reads endpoint groups: rooms, floors and zones that providers report, and
// groups the customer made.
type GroupCommand struct {
	apiClient apiclient.Client
	formatter output.Formatter
}

// NewGroupCommand creates a new group command.
func NewGroupCommand(apiClient apiclient.Client, formatter output.Formatter) *GroupCommand {
	return &GroupCommand{apiClient: apiClient, formatter: formatter}
}

// GroupListParams contains parameters for the group List command.
type GroupListParams struct {
	MaxResults *int
	NextToken  *string
	// Expand passes the API's expansion tokens, such as interfaces.attributes.
	Expand []string
}

// GroupListResult is the JSON document group list prints: the API's groups unchanged, so a
// script reads controllers and each group route's attributes (spatial/geometry) as the API
// documents them.
type GroupListResult struct {
	Groups    []api.EndpointGroup `json:"groups"`
	NextToken *string             `json:"next_token,omitempty"`
}

// List prints one page of the caller's enabled groups.
func (c *GroupCommand) List(ctx context.Context, params GroupListParams) error {
	response, err := c.apiClient.Groups().Enumerate(ctx, params.NextToken, params.MaxResults, params.Expand)
	if err != nil {
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && params.NextToken != nil {
			return fmt.Errorf("pagination token is invalid or expired — start a new paginated request without --next-token")
		}
		return fmt.Errorf("failed to list groups: %w", err)
	}
	result := GroupListResult{Groups: response.Results}
	if result.Groups == nil {
		result.Groups = []api.EndpointGroup{}
	}
	if response.PaginationContext != nil && response.PaginationContext.NextToken != nil && *response.PaginationContext.NextToken != "" {
		result.NextToken = response.PaginationContext.NextToken
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	return c.formatter.Print(groupListText(result))
}

func groupListText(result GroupListResult) string {
	if len(result.Groups) == 0 {
		return "No groups found.\n"
	}
	var sb strings.Builder
	for _, group := range result.Groups {
		members, controllers := 0, 0
		if group.MemberEndpointIds != nil {
			members = len(*group.MemberEndpointIds)
		}
		if group.Controllers != nil {
			controllers = len(*group.Controllers)
		}
		fmt.Fprintf(&sb, "%s  %s  members %d, controllers %d\n", group.Name.Value, group.Id, members, controllers)
	}
	if result.NextToken != nil {
		fmt.Fprintf(&sb, "\nMore groups: --next-token %s\n", *result.NextToken)
	}
	return sb.String()
}
