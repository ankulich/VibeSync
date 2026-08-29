// Package authclient adapts the Auth Service's Connect client for fetching
// per-user provider tokens.
package authclient

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	"vibesync/apps/provider-service/internal/ports"
	authv1 "vibesync/gen/go/vibesync/auth/v1"
	authv1connect "vibesync/gen/go/vibesync/auth/v1/authv1connect"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// Client fetches per-user provider access tokens from the Auth Service.
type Client struct {
	client authv1connect.AuthServiceClient
}

// NewClient constructs the adapter. baseURL is the Auth Service origin, e.g.
// "http://localhost:8080".
func NewClient(httpClient *http.Client, baseURL string) *Client {
	return &Client{client: authv1connect.NewAuthServiceClient(httpClient, baseURL)}
}

// GetUserToken returns a fresh access token for the user's linked provider
// account. The Auth Service refreshes expired grants internally.
func (c *Client) GetUserToken(ctx context.Context, userID, provider string) (string, error) {
	if userID == "" || provider == "" {
		return "", fmt.Errorf("authclient: user_id and provider are required")
	}
	resp, err := c.client.GetProviderToken(ctx, connect.NewRequest(&authv1.GetProviderTokenRequest{
		UserId:   &commonv1.Id{Value: userID},
		Provider: provider,
	}))
	if err != nil {
		return "", fmt.Errorf("authclient: get provider token: %w", err)
	}
	token := resp.Msg.GetAccessToken()
	if token == "" {
		return "", fmt.Errorf("authclient: get provider token: empty access token")
	}
	return token, nil
}

var _ ports.TokenSource = (*Client)(nil)
