package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"vibesync/apps/media-service/internal/ports"
)

// mapErr translates pgx errors into ports.ErrNotFound for not-found rows.
func mapErr(entity, id string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.NotFound(entity, id)
	}
	return fmt.Errorf("%s: %w", entity, err)
}
