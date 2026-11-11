package transport

import (
	"context"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
)

// cloudSender sends through the Port OS API.
type cloudSender struct {
	client apiclient.Client
}

func (c *cloudSender) Send(ctx context.Context, req *api.CreateMessageRequest, idempotencyKey string) (*api.MessageRequest, error) {
	return c.client.Messages().Send(ctx, req, idempotencyKey)
}

// Cloud returns a Sender that always goes to Port OS.
func Cloud(client apiclient.Client) Sender { return &cloudSender{client: client} }

type cloudResolver struct{ sender Sender }

func (c cloudResolver) For(context.Context, string, string) (Sender, error) { return c.sender, nil }

// CloudOnly returns a Resolver that never consults a session host.
func CloudOnly(client apiclient.Client) Resolver { return cloudResolver{sender: Cloud(client)} }
