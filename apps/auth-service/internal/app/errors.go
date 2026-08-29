package app

import (
	"errors"

	"vibesync/apps/auth-service/internal/ports"
)

// isNotFound reports whether err is a ports.NotFound error (set by every
// repository's not-found branch). Use cases branch on this to map not-found
// to the appropriate client-visible code (typically Unauthenticated for
// credential lookups, NotFound for direct resource fetches).
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ports.ErrNotFound)
}
