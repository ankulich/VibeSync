// Package roomclient adapts the Room Service's Connect client for member
// permission checks (ADR-0017).
package roomclient

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	"vibesync/apps/sync-service/internal/ports"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	roomv1connect "vibesync/gen/go/vibesync/room/v1/roomv1connect"
)

// Client asks the Room Service whether a user holds a room permission.
type Client struct {
	client roomv1connect.RoomServiceClient
}

// NewClient constructs the adapter. baseURL is the Room Service origin,
// e.g. "http://localhost:8082".
func NewClient(httpClient *http.Client, baseURL string) *Client {
	return &Client{client: roomv1connect.NewRoomServiceClient(httpClient, baseURL)}
}

// Has reports whether userID holds perm in the room. The Room Service
// answers true for the room owner regardless of stored grants.
func (c *Client) Has(ctx context.Context, roomID, userID string, perm roomv1.RoomPermission) (bool, error) {
	if roomID == "" || userID == "" {
		return false, fmt.Errorf("roomclient: room_id and user_id are required")
	}
	resp, err := c.client.GetMemberPermissions(ctx, connect.NewRequest(&roomv1.GetMemberPermissionsRequest{
		RoomId: &commonv1.Id{Value: roomID},
		UserId: &commonv1.Id{Value: userID},
	}))
	if err != nil {
		return false, fmt.Errorf("roomclient: get member permissions: %w", err)
	}
	if resp.Msg.GetIsOwner() {
		return true, nil
	}
	for _, p := range resp.Msg.GetPermissions() {
		if p == perm {
			return true, nil
		}
	}
	return false, nil
}

var _ ports.RoomPermissions = (*Client)(nil)
