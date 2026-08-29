package app

// Placeholder removed. Handler methods live in their per-use-case files:
//   - login.go      → Login
//   - refresh.go    → RefreshToken, Logout
//   - oauth.go      → BeginOAuth, CompleteOAuth
//   - keys.go       → RotateKeys, GetJwks
//   - introspect.go → Introspect
//
// The full *Service → authv1connect.AuthServiceHandler interface assertion
// lives at the bottom of introspect.go (the last method file) so a missing
// method fails the build with a clear error pointing at the interface.
