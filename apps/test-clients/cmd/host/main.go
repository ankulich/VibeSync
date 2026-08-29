// Command host is a test client that acts as the room host: it logs in,
// creates a room, loads a test media item, subscribes to the sync stream,
// issues playback commands, and prints every authoritative state it receives.
//
// Usage:
//
//	go run ./apps/test-clients/cmd/host \
//	    -email alice@test.local -password secret -name "Alice"
//
// It prints the created room ID — pass it to the listener client with -room.
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
	mediav1 "vibesync/gen/go/vibesync/media/v1"
	"vibesync/gen/go/vibesync/media/v1/mediav1connect"
	roomv1 "vibesync/gen/go/vibesync/room/v1"
	"vibesync/gen/go/vibesync/room/v1/roomv1connect"
	syncv1 "vibesync/gen/go/vibesync/sync/v1"
	"vibesync/gen/go/vibesync/sync/v1/syncv1connect"
)

var (
	authURL  = flag.String("auth", "http://localhost:8080", "auth service URL")
	roomURL  = flag.String("room", "http://localhost:8082", "room service URL")
	syncURL  = flag.String("sync", "http://localhost:8083", "sync service URL")
	mediaURL = flag.String("media", "http://localhost:8085", "media service URL")
	email    = flag.String("email", "host@test.local", "login email")
	password = flag.String("password", "testpass123", "login password")
	roomName = flag.String("name", "Test Room", "room name to create")
	interval = flag.Duration("interval", 5*time.Second, "time between commands")
)

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl-C stops the client.
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
		Email: *email, Password: *password, DeviceLabel: ptr("test-host"),
	}))
	fatalIf(err, "login")
	userID := loginResp.Msg.GetSession().GetUserId().GetValue()
	accessToken := loginResp.Msg.GetSession().GetTokens().GetAccessToken()
	fmt.Printf("    user: %s\n", userID)

	// 2. Create a room.
	fmt.Println("=== 2. Create room ===")
	roomClient := roomv1connect.NewRoomServiceClient(withAuth(newHTTP(), accessToken, userID), *roomURL)
	roomResp, err := roomClient.CreateRoom(ctx, connect.NewRequest(&roomv1.CreateRoomRequest{
		Name: *roomName,
	}))
	fatalIf(err, "create room")
	roomID := roomResp.Msg.GetRoom().GetId().GetValue()
	fmt.Printf("    room: %s (slug %s)\n", roomID, roomResp.Msg.GetRoom().GetSlug())

	// 3. Create a media item + add to queue.
	fmt.Println("=== 3. Create media + queue ===")
	mediaClient := mediav1connect.NewMediaServiceClient(withAuth(newHTTP(), accessToken, userID), *mediaURL)
	mediaResp, err := mediaClient.CreateMedia(ctx, connect.NewRequest(&mediav1.CreateMediaRequest{
		Kind:       mediav1.MediaKind_MEDIA_KIND_AUDIO,
		Source:     mediav1.MediaSource_MEDIA_SOURCE_PROVIDER,
		Title:      "Test Track",
		Artist:     "Test Artist",
		DurationMs: 180_000,
	}))
	fatalIf(err, "create media")
	mediaID := mediaResp.Msg.GetMedia().GetId().GetValue()
	fmt.Printf("    media: %s (%s)\n", mediaID, mediaResp.Msg.GetMedia().GetTitle())
	_, err = mediaClient.AddToQueue(ctx, connect.NewRequest(&mediav1.AddToQueueRequest{
		RoomId: id(roomID), MediaId: id(mediaID),
	}))
	fatalIf(err, "add to queue")

	// 4. Subscribe to the sync stream.
	fmt.Println("=== 4. Subscribe to sync stream ===")
	syncClient := syncv1connect.NewSyncServiceClient(withAuth(newHTTP(), accessToken, userID), *syncURL)
	stream, err := syncClient.Subscribe(ctx, connect.NewRequest(&syncv1.SubscribeRequest{
		RoomId: id(roomID), LastAppliedEpoch: 0,
	}))
	fatalIf(err, "subscribe")

	// Print frames in the background.
	go func() {
		for stream.Receive() {
			frame := stream.Msg()
			printFrame("recv", frame)
		}
		if err := stream.Err(); err != nil {
			log.Printf("stream error: %v", err)
		}
	}()

	// 5. Issue commands on an interval: LOAD → PLAY → PAUSE → PLAY → SEEK...
	fmt.Printf("=== 5. Issuing commands every %s ===\n", *interval)
	commands := []struct {
		kind syncv1.CommandKind
		seek *int64
	}{
		{syncv1.CommandKind_COMMAND_KIND_LOAD_MEDIA, nil},
		{syncv1.CommandKind_COMMAND_KIND_PLAY, nil},
		{syncv1.CommandKind_COMMAND_KIND_PAUSE, nil},
		{syncv1.CommandKind_COMMAND_KIND_PLAY, nil},
		{syncv1.CommandKind_COMMAND_KIND_SEEK, ptr(int64(60_000))},
		{syncv1.CommandKind_COMMAND_KIND_PLAY, nil},
	}
	fencingToken := uint64(0)
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
		cmd := commands[i%len(commands)]
		if cmd.kind == syncv1.CommandKind_COMMAND_KIND_LOAD_MEDIA {
			cmdResp, err := syncClient.Command(ctx, connect.NewRequest(&syncv1.CommandRequest{
				RoomId: id(roomID), Kind: cmd.kind,
				MediaId: id(mediaID), FencingToken: fencingToken,
			}))
			if err != nil {
				log.Printf("command error: %v", err)
				continue
			}
			if cmdResp.Msg.GetAccepted() {
				fencingToken = cmdResp.Msg.GetEpoch() // track for future commands
			}
			fmt.Printf("[cmd] %-11s epoch=%d accepted=%v\n",
				cmd.kind.String(), cmdResp.Msg.GetEpoch(), cmdResp.Msg.GetAccepted())
			continue
		}
		cmdResp, err := syncClient.Command(ctx, connect.NewRequest(&syncv1.CommandRequest{
			RoomId: id(roomID), Kind: cmd.kind,
			SeekToMs: cmd.seek, FencingToken: fencingToken,
		}))
		if err != nil {
			log.Printf("command error: %v", err)
			continue
		}
		fmt.Printf("[cmd] %-45s epoch=%d accepted=%v\n",
			cmd.kind.String(), cmdResp.Msg.GetEpoch(), cmdResp.Msg.GetAccepted())
	}
}

// printFrame pretty-prints a SubscribeResponse frame.
func printFrame(prefix string, frame *syncv1.SubscribeResponse) {
	switch p := frame.GetPayload().(type) {
	case *syncv1.SubscribeResponse_Update:
		s := p.Update
		fmt.Printf("[%s] update   epoch=%-3d status=%-7s pos=%-8d rate=%.1f host=%s\n",
			prefix, s.GetEpoch(), s.GetStatus().String(), s.GetMediaTimeMs(),
			s.GetPlaybackRate(), short(s.GetHostId().GetValue()))
	case *syncv1.SubscribeResponse_Snapshot:
		s := p.Snapshot
		fmt.Printf("[%s] snapshot  epoch=%-3d drift=%-4dms conf=%-3d peers=%d rtt=%dms\n",
			prefix, s.GetState().GetEpoch(), s.GetDriftEstimateMs(),
			s.GetConfidence(), s.GetActivePeers(), s.GetMedianRttMs())
	case *syncv1.SubscribeResponse_HostMigration:
		m := p.HostMigration
		fmt.Printf("[%s] migration new_host=%s epoch=%d token=%d\n",
			prefix, short(m.GetNewHostId().GetValue()), m.GetNewEpoch(), m.GetNewFencingToken())
	}
}

// --- helpers ---

func newHTTP() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// authTransport wraps an http.RoundTripper adding auth + subject headers.
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
