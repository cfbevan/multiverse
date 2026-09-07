package postgres

import (
	"context"
	"database/sql"

	"github.com/cfbevan/multiverse/internal/models"
)

// CommentModel persists comment records.
type CommentModel struct {
	DB *sql.DB
}

// Insert stores a new comment.
func (m CommentModel) Insert(ctx context.Context, comment *models.Comment) error {
	query := `
INSERT INTO comments (entity_type, entity_id, actor_id, content, reply_to_comment_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at, updated_at`

	return m.DB.QueryRowContext(
		ctx,
		query,
		comment.EntityType,
		comment.EntityID,
		comment.ActorID,
		comment.Content,
		comment.ReplyToCommentID,
	).Scan(&comment.ID, &comment.CreatedAt, &comment.UpdatedAt)
}

// ListByEntity lists comments for a specific entity.
func (m CommentModel) ListByEntity(
	ctx context.Context,
	entityType string,
	entityID int64,
	limit, offset int,
) ([]models.Comment, error) {
	query := `
SELECT id, entity_type, entity_id, actor_id, content, reply_to_comment_id, created_at, updated_at, deleted_at
FROM comments
WHERE deleted_at IS NULL AND entity_type = $1 AND entity_id = $2
ORDER BY created_at ASC
LIMIT $3 OFFSET $4`

	rows, err := m.DB.QueryContext(ctx, query, entityType, entityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	comments := make([]models.Comment, 0)
	for rows.Next() {
		var c models.Comment
		if err := rows.Scan(
			&c.ID,
			&c.EntityType,
			&c.EntityID,
			&c.ActorID,
			&c.Content,
			&c.ReplyToCommentID,
			&c.CreatedAt,
			&c.UpdatedAt,
			&c.DeletedAt,
		); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return comments, nil
}
