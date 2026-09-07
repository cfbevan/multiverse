package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
)

// OutboxEventModel persists outbound ActivityPub delivery events.
type OutboxEventModel struct {
	DB *sql.DB
}

// Enqueue adds a new outbound event to the queue.
func (m OutboxEventModel) Enqueue(ctx context.Context, event *models.OutboxEvent) error {
	query := `
INSERT INTO outbox_events (actor_id, target_inbox_url, target_domain, idempotency_key, payload, status, attempts, next_attempt_at)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
RETURNING id, created_at`

	return m.DB.QueryRowContext(ctx, query,
		event.ActorID,
		event.TargetInboxURL,
		event.TargetDomain,
		event.IdempotencyKey,
		event.Payload,
		event.Status,
		event.Attempts,
		event.NextAttemptAt,
	).Scan(&event.ID, &event.CreatedAt)
}

func scanOutboxEvents(rows *sql.Rows) ([]models.OutboxEvent, error) {
	return scanRows(rows, func(row *sql.Rows, e *models.OutboxEvent) error {
		return row.Scan(
			&e.ID,
			&e.ActorID,
			&e.TargetInboxURL,
			&e.TargetDomain,
			&e.IdempotencyKey,
			&e.Payload,
			&e.Status,
			&e.Attempts,
			&e.NextAttemptAt,
			&e.LastError,
			&e.SentAt,
			&e.CreatedAt,
		)
	})
}

// ClaimDue claims queued outbound delivery jobs due for processing.
func (m OutboxEventModel) ClaimDue(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]models.OutboxEvent, error) {
	query := `
WITH due AS (
    SELECT id
    FROM outbox_events
    WHERE status IN ('queued', 'failed')
      AND next_attempt_at <= $1
    ORDER BY next_attempt_at ASC
    LIMIT $2
    FOR UPDATE SKIP LOCKED
), claimed AS (
    UPDATE outbox_events o
    SET status = 'processing',
        attempts = o.attempts + 1
    FROM due
    WHERE o.id = due.id
    RETURNING o.id, o.actor_id, o.target_inbox_url, o.target_domain, o.idempotency_key, o.payload, o.status, o.attempts, o.next_attempt_at, o.last_error, o.sent_at, o.created_at
)
SELECT id, actor_id, target_inbox_url, target_domain, idempotency_key, payload, status, attempts, next_attempt_at, COALESCE(last_error, ''), sent_at, created_at
FROM claimed`

	rows, err := m.DB.QueryContext(ctx, query, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanOutboxEvents(rows)
}

// MarkSent marks an outbound event as sent.
func (m OutboxEventModel) MarkSent(ctx context.Context, id string, sentAt time.Time) error {
	const query = `
UPDATE outbox_events
SET status = 'sent', sent_at = $2, last_error = NULL
WHERE id = $1`
	_, err := m.DB.ExecContext(ctx, query, id, sentAt)

	return err
}

// MarkFailed marks an outbound event as failed and schedules a retry.
func (m OutboxEventModel) MarkFailed(
	ctx context.Context,
	id string,
	lastErr string,
	nextAttemptAt time.Time,
) error {
	const query = `
UPDATE outbox_events
SET status = 'failed', last_error = $2, next_attempt_at = $3
WHERE id = $1`
	_, err := m.DB.ExecContext(ctx, query, id, lastErr, nextAttemptAt)

	return err
}
