package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/lib/pq"
)

// InboxEventModel persists inbound ActivityPub events.
type InboxEventModel struct {
	DB *sql.DB
}

// Insert stores a new inbound inbox event.
func (m InboxEventModel) Insert(ctx context.Context, event *models.InboxEvent) error {
	query := `
INSERT INTO inbox_events (actor_id, source_domain, signature_key_id, digest, request_id, idempotency_key, payload, status)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
RETURNING id, received_at`

	if err := m.DB.QueryRowContext(ctx, query,
		event.ActorID,
		event.SourceDomain,
		event.SignatureKeyID,
		event.Digest,
		event.RequestID,
		event.IdempotencyKey,
		event.Payload,
		event.Status,
	).Scan(&event.ID, &event.ReceivedAt); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return models.ErrEditConflict
		}

		return err
	}

	return nil
}
