// Package audit records who did what, in the same transaction as the change itself.
// The audit_events table is created by the core migrations.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/db"
)

// Event is one audit record. Details must not contain secrets or full request bodies.
type Event struct {
	OrgID        string         `json:"org_id,omitempty"`
	ActorUserID  string         `json:"actor_user_id,omitempty"`
	ActorTokenID string         `json:"actor_token_id,omitempty"`
	Action       string         `json:"action"` // e.g. "member.removed"
	TargetType   string         `json:"target_type,omitempty"`
	TargetID     string         `json:"target_id,omitempty"`
	IP           string         `json:"ip,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
}

// Record writes an event. Pass the transaction that makes the change.
func Record(ctx context.Context, q db.DBTX, e Event) error {
	details, err := json.Marshal(e.Details)
	if err != nil {
		return fmt.Errorf("audit: details: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_events (org_id, actor_user_id, actor_token_id, action, target_type, target_id, ip, details)
		VALUES (NULLIF($1, '')::uuid, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, $5, $6, NULLIF($7, '')::inet, $8)`,
		e.OrgID, e.ActorUserID, e.ActorTokenID, e.Action, e.TargetType, e.TargetID, e.IP, details)
	if err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

// Entry is a stored event.
type Entry struct {
	ID int64 `json:"id"`
	Event
	CreatedAt time.Time `json:"created_at"`
}

// List returns an organization's events, newest first. before is the ID of the last entry of the
// previous page (0 for the first page).
func List(ctx context.Context, q db.DBTX, orgID string, before int64, limit int) ([]Entry, error) {
	rows, err := q.Query(ctx, `
		SELECT id, coalesce(org_id::text, ''), coalesce(actor_user_id::text, ''), coalesce(actor_token_id::text, ''),
		       action, target_type, target_id, coalesce(host(ip), ''), details, created_at
		FROM audit_events
		WHERE org_id = $1 AND ($2 = 0 OR id < $2)
		ORDER BY id DESC
		LIMIT $3`, orgID, before, limit)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Entry, error) {
		var e Entry
		var details []byte
		err := r.Scan(&e.ID, &e.OrgID, &e.ActorUserID, &e.ActorTokenID, &e.Action, &e.TargetType, &e.TargetID, &e.IP, &details, &e.CreatedAt)
		if err == nil && len(details) > 0 && string(details) != "null" {
			err = json.Unmarshal(details, &e.Details)
		}
		return e, err
	})
}

// Purge deletes events older than the retention period.
func Purge(ctx context.Context, q db.DBTX, olderThan time.Duration) error {
	_, err := q.Exec(ctx, "DELETE FROM audit_events WHERE created_at < now() - $1::interval", olderThan.String())
	return err
}
