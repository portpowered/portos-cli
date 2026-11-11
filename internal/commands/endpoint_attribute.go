package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
)

// AttributeParams contains parameters for the Attribute command.
type AttributeParams struct {
	EndpointID string
	// Attribute is "<interface>/<attribute>", for example spatial/pose.
	Attribute string
	// Force asks the server to refresh the value from the device first.
	Force bool
}

// Attribute prints one attribute's value for an endpoint. With Force the server
// reads the device instead of answering from stored state.
func (c *EndpointCommand) Attribute(ctx context.Context, params AttributeParams) error {
	if params.EndpointID == "" {
		return exitcode.NewInvalidArgument(errors.New("endpoint ID is required"))
	}
	slash := strings.LastIndex(params.Attribute, "/")
	if slash <= 0 || slash == len(params.Attribute)-1 {
		return exitcode.NewInvalidArgument(fmt.Errorf("attribute %q must be <interface>/<attribute>, for example spatial/pose", params.Attribute))
	}
	iface, name := params.Attribute[:slash], params.Attribute[slash+1:]

	response, err := c.apiClient.Endpoints().GetByIDWithExpand(ctx, params.EndpointID, "interfaces.attributes", params.Force)
	if err != nil {
		return fmt.Errorf("failed to get endpoint: %w", err)
	}
	if len(response.Results) == 0 {
		return fmt.Errorf("endpoint not found: %s", params.EndpointID)
	}

	notReported := fmt.Errorf("attribute %s not reported", params.Attribute)
	if response.Results[0].Attributes == nil {
		return notReported
	}
	for _, attr := range *response.Results[0].Attributes {
		if attr.Name != name || !(attr.Namespace == iface || strings.HasSuffix(attr.Namespace, "/"+iface)) {
			continue
		}
		if attr.Error != nil {
			return fmt.Errorf("attribute %s could not be read: %s", params.Attribute, attr.Error.Message)
		}
		if attr.Value == nil {
			return notReported
		}
		c.warnIfNotFresh(params.Attribute, attr)
		return c.formatter.Print(*attr.Value)
	}
	return notReported
}

// warnIfNotFresh writes one stderr line when the value is not current, so a stale
// reading is not mistaken for the device's present state. Stdout stays the bare value.
func (c *EndpointCommand) warnIfNotFresh(name string, attr api.AttributeState) {
	if attr.Freshness == nil || *attr.Freshness == api.FRESH {
		return
	}
	sampled := "unknown"
	if attr.SampledAt != nil {
		sampled = attr.SampledAt.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(c.stderr, "warning: attribute %s freshness %s, sampledAt %s; the value may not be the device's present state\n", name, *attr.Freshness, sampled)
}
