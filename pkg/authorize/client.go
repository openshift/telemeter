package authorize

import (
	"context"
)

type ClientAuthorizer interface {
	AuthorizeClient(token string) (*Client, bool, error)
}

type Client struct {
	ID     string
	Labels map[string]string
}

func WithClient(ctx context.Context, client *Client) context.Context {
	return context.WithValue(ctx, clientKey, client)
}

func FromContext(ctx context.Context) (*Client, bool) {
	client, ok := ctx.Value(clientKey).(*Client)
	return client, ok
}
