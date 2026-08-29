// Command seed creates two test users (host + listener) in the auth database
// so the test clients can login with email/password. The auth service has no
// registration RPC (users are created via OAuth); this tool inserts them
// directly with argon2id-hashed passwords (same parameters as the auth
// service's PasswordHasher).
//
// Usage:
//
//	go run ./apps/test-clients/cmd/seed
//
// Requires Postgres on localhost:5432 (vibesync/vibesync, database "auth").
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
)

var (
	dbURL = flag.String("db", "postgres://vibesync:vibesync@localhost:5432/auth?sslmode=disable", "auth database URL")
)

// testUsers are the two accounts the test clients login with.
var testUsers = []struct {
	email    string
	username string
	name     string
}{
	{"host@test.local", "test_host", "Test Host"},
	{"listener@test.local", "test_listener", "Test Listener"},
}

// argon2 parameters — must match the auth service's PasswordHasher
// (internal/infra/crypto/password.go).
const (
	argon2Memory     = 19 * 1024
	argon2Iterations = 2
	argon2Lanes      = 1
	argon2SaltBytes  = 16
	argon2KeyBytes   = 32
)

const testPassword = "testpass123"

func main() {
	flag.Parse()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, *dbURL)
	if err != nil {
		log.Fatalf("connect: %v (is Postgres running? make docker-up)", err)
	}
	defer conn.Close(context.Background())

	for _, u := range testUsers {
		if err := seedUser(ctx, conn, u.email, u.username, u.name); err != nil {
			log.Fatalf("seed %s: %v", u.email, err)
		}
	}

	fmt.Println("OK: seeded 2 test users (password: " + testPassword + ")")
	fmt.Println("    host@test.local     / test_host")
	fmt.Println("    listener@test.local / test_listener")
}

func seedUser(ctx context.Context, conn *pgx.Conn, email, username, name string) error {
	hash, err := hashPassword(testPassword)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}

	// ULID-ish ID: timestamp-based, 26 chars, unique enough for seeding.
	id := fmt.Sprintf("01TEST%-20s", username)[:26]

	tag, err := conn.Exec(ctx, `
		INSERT INTO auth.users (id, email, username, display_name, password_hash, system_role, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 2, 1, now(), now())
		ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash`,
		id, email, username, name, hash)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	if tag.RowsAffected() > 0 {
		fmt.Printf("  seeded: %-22s (id %s)\n", email, id)
	} else {
		fmt.Printf("  exists: %s (password updated)\n", email)
	}
	return nil
}

// hashPassword produces the same PHC-string format as the auth service.
func hashPassword(plaintext string) (string, error) {
	salt := make([]byte, argon2SaltBytes)
	// Fixed salt for deterministic seeding (test-only; production uses crypto/rand).
	for i := range salt {
		salt[i] = byte(i*7 + 13)
	}
	key := argon2.IDKey([]byte(plaintext), salt, argon2Iterations, argon2Memory, argon2Lanes, argon2KeyBytes)
	return phcEncode(salt, key), nil
}

// phcEncode produces the PHC-string form (matches the auth service format).
func phcEncode(salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Iterations, argon2Lanes,
		base64RawStd(salt), base64RawStd(key),
	)
}

func base64RawStd(b []byte) string {
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}
