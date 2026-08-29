// Command listener is a test client that acts as a room listener: it logs in,
// joins the room created by the host client, subscribes to the sync stream,
// sends heartbeats every second (simulating a media player), and prints the
// authoritative state + per-client drift/RTT from the heartbeat responses.
//
// Usage:
//
//	go run ./apps/test-clients/cmd/listener \
//	    -email bob@test.local -password secret -room <room-id-from-host>
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"connectrpc.com/connect"

	authv1 "vibesync/gen/go/vibesync/auth/v1"
	"vibesync/gen/go/vibesync/auth/v1/authv1connect"
	commonv1 "vibesync/gen/go/vibesync/common/v1"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	"vibesync/gen/go/vibesync/room/v1/roomv1connect"
	syncv1 "vibesync/gen/go/vibesync/sync/v1"
	"vibesync/gen/go/vibesync/sync/v1/syncv1connect"
)

var (
	authURL  = flag.String("auth", "http://localhost:8080", "auth service URL")
	roomURL  = flag.String("room", "http://localhost:8082", "room service URL")
	syncURL  = flag.String("sync", "http://localhost:8083", "sync service URL")
	email    = flag.String("email", "listener@test.local", "login email")
	password = flag.String("password", "testpass123", "login password")
	roomID   = flag.String("room", "", "room ID to join (from the host client output)")
	drift    = flag.Duration("drift", 0, "simulated clock drift (positive = client ahead)")
)

func main() {
	flag.Parse()
	if *roomID == "" {
		log.Fatal("-room is required (get the room ID from the host client output)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\n--- shutting down ---")
		cancel()
	}()

	// 1. Login.
	fmt.Println("=== 1. Login ===")
	authClient := authv1connect.NewAuthServiceClient(newHTTP(), *authURL)
	loginResp, err := authClient.Login(ctx, connect.NewRequest(&authv1.LoginRequest{
		Email: *email, Password: *password, DeviceLabel: ptr("test-listener"),
	}))
	fatalIf(err, "login")
	userID := loginResp.Msg.GetSession().GetUserId().GetValue()
	accessToken := loginResp.Msg.GetSession().GetTokens().GetAccessToken()
	fmt.Printf("    user: %s\n", userID)

	authedCl := withAuth(newHTTP(), accessToken, userID)

	// 2. Join the room.
	fmt.Printf("=== 2. Join room %s ===\n", short(*roomID))
	roomClient := roomv1connect.NewRoomServiceClient(authedCl, *roomURL)
	joinResp, err := roomClient.JoinRoom(ctx, connect.NewRequest(&roomv1.JoinRoomRequest{
		RoomId: id(*roomID),
	}))
	fatalIf(err, "join room")
	fmt.Printf("    joined as %s\n", joinResp.Msg.GetAssignedRole().String())

	// 3. Subscribe to the sync stream.
	fmt.Println("=== 3. Subscribe to sync stream ===")
	syncClient := syncv1connect.NewSyncServiceClient(authedCl, *syncURL)
	stream, err := syncClient.Subscribe(ctx, connect.NewRequest(&syncv1.SubscribeRequest{
		RoomId: id(*roomID), LastAppliedEpoch: 0,
	}))
	fatalIf(err, "subscribe")

	// Track the latest authoritative state so the simulated player can report
	// its position against it.
	var lastState *syncv1.SyncState
	var lastEpoch uint64

	// Print frames in the background.
	go func() {
		for stream.Receive() {
			frame := stream.Msg()
			switch p := frame.GetPayload().(type) {
			case *syncv1.SubscribeResponse_Update:
				lastState = p.Update
				if p.Update.GetEpoch() >= lastEpoch {
					lastEpoch = p.Update.GetEpoch()
				}
				fmt.Printf("[recv] update   epoch=%-3d status=%-7s pos=%-8d rate=%.1f host=%s\n",
					p.Update.GetEpoch(), p.Update.GetStatus().String(),
					p.Update.GetMediaTimeMs(), p.Update.GetPlaybackRate(),
					short(p.Update.GetHostId().GetValue()))
			case *syncv1.SubscribeResponse_Snapshot:
				lastState = p.Snapshot.GetState()
				fmt.Printf("[recv] snapshot  epoch=%-3d drift=%-4dms conf=%-3d peers=%d rtt=%dms\n",
					p.Snapshot.GetState().GetEpoch(), p.Snapshot.GetDriftEstimateMs(),
					p.Snapshot.GetConfidence(), p.Snapshot.GetActivePeers(),
					p.Snapshot.GetMedianRttMs())
			case *syncv1.SubscribeResponse_HostMigration:
				fmt.Printf("[recv] migration new_host=%s epoch=%d\n",
					short(p.HostMigration.GetNewHostId().GetValue()), p.HostMigration.GetNewEpoch())
			}
		}
		if err := stream.Err(); err != nil {
			log.Printf("stream error: %v", err)
		}
	}()

	// 4. Send heartbeats every second (simulated media player).
	fmt.Println("=== 4. Heartbeats (1s interval) ===")
	var lastServerWallTime int64
	simulatedDriftMs := drift.Milliseconds()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeatCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Simulate the client's media position: advance the authoritative
		// position by 1s (at rate 1.0), plus the configured drift.
		var clientMediaTime int64
		if lastState != nil && lastState.GetPlaybackRate() > 0 {
			nowMs := time.Now().UnixMilli()
			elapsed := nowMs - lastState.GetWallTimeMs()
			if elapsed < 0 {
				elapsed = 0
			}
			clientMediaTime = lastState.GetMediaTimeMs() + int64(float64(elapsed)*lastState.GetPlaybackRate())
		}
		clientMediaTime += simulatedDriftMs

		hbResp, err := syncClient.Heartbeat(ctx, connect.NewRequest(&syncv1.HeartbeatRequest{
			RoomId:               id(*roomID),
			ClientEpoch:          lastEpoch,
			ClientMediaTimeMs:    clientMediaTime,
			ClientWallTimeMs:     time.Now().UnixMilli(),
			LastServerWallTimeMs: lastServerWallTime,
		}))
		if err != nil {
			log.Printf("heartbeat error: %v", err)
			continue
		}
		lastServerWallTime = hbResp.Msg.GetServerWallTimeMs()
		heartbeatCount++

		// Print heartbeat stats every 5 beats (avoid flooding).
		if heartbeatCount%5 == 1 {
			fmt.Printf("[hb]   #%-4d drift=%-+5dms rtt=%-4dms server_pos=%d\n",
				heartbeatCount, hbResp.Msg.GetClientDriftMs(),
				hbResp.Msg.GetSmoothedRttMs(), hbResp.Msg.GetServerMediaTimeMs())
		}
	}
}

// --- helpers (same as host) ---

func newHTTP() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

type authTransport struct {
	base   http.RoundTripper
	token  string
	userID string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("X-Vibesync-User-Id", t.userID)
	req.Header.Set("X-Vibesync-System-Role", "SYSTEM_ROLE_USER")
	return t.base.RoundTrip(req)
}

func withAuth(c *http.Client, token, userID string) *http.Client {
	return &http.Client{
		Timeout:   c.Timeout,
		Transport: &authTransport{base: http.DefaultTransport, token: token, userID: userID},
	}
}

func id(v string) *commonv1.Id { return &commonv1.Id{Value: v} }
func short(v string) string {
	if len(v) > 8 {
		return v[:8]
	}
	return v
}
func ptr[T any](v T) *T { return &v }
func fatalIf(err error, what string) {
	if err != nil {
		log.Fatalf("%s failed: %v", what, err)
	}
}
