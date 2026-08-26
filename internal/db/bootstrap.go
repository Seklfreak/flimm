package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BootstrapDevUser ensures the fixed local dev user (AUTH_DISABLED) exists so
// the foreign keys on feeds / watch_events / user_prefs resolve. Idempotent.
func BootstrapDevUser(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, sub string) error {
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, oidc_sub, name) VALUES ($1, $2, 'Dev User') ON CONFLICT (oidc_sub) DO NOTHING`,
		id, sub); err != nil {
		return fmt.Errorf("ensure dev user: %w", err)
	}
	return nil
}
