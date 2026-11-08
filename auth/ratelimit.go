package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/mahrus-rohisyam/core-backend/db"
)

// Limiter counts attempts per key in fixed time windows, stored in Postgres so that every app
// copy shares the same counters. The rate_limits table is created by the core migrations.
type Limiter struct {
	db  db.DBTX
	now func() time.Time
}

// NewLimiter returns a Limiter backed by the given database.
func NewLimiter(d db.DBTX) *Limiter { return &Limiter{db: d, now: time.Now} }

// Allow records one attempt for key and reports whether it is within limit attempts per window.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	start := l.now().Truncate(window)
	var count int
	err := l.db.QueryRow(ctx, `
		INSERT INTO rate_limits (key, window_start, count, expires_at)
		VALUES ($1, $2, 1, $3)
		ON CONFLICT (key, window_start) DO UPDATE SET count = rate_limits.count + 1
		RETURNING count`, key, start, start.Add(window)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("auth: rate limit: %w", err)
	}
	return count <= limit, nil
}

// Reset clears the counters for key, for example after a successful sign-in.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	_, err := l.db.Exec(ctx, "DELETE FROM rate_limits WHERE key = $1", key)
	return err
}

// Purge deletes expired windows. The app calls it periodically.
func (l *Limiter) Purge(ctx context.Context) error {
	_, err := l.db.Exec(ctx, "DELETE FROM rate_limits WHERE expires_at < now()")
	return err
}
