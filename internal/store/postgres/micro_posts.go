package postgres

import (
	"context"
	"database/sql"

	"github.com/cfbevan/multiverse/internal/models"
)

// MicroPostModel handles persistence for micro posts.
type MicroPostModel struct {
	DB *sql.DB
}

// Insert stores a new micro post and fills in the persisted metadata.
func (m MicroPostModel) Insert(ctx context.Context, post *models.MicroPost) error {
	query := `
INSERT INTO micro_posts (actor_id, content, visibility, reply_to_micro_post_id, ap_object_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, published_at, updated_at, version`

	return m.DB.QueryRowContext(
		ctx,
		query,
		post.ActorID,
		post.Content,
		post.Visibility,
		post.ReplyToMicroPostID,
		post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version)
}

// ListPublic returns public micro posts ordered newest first.
func (m MicroPostModel) ListPublic(
	ctx context.Context,
	limit, offset int,
) ([]models.MicroPost, error) {
	query := `
SELECT id, actor_id, content, visibility, reply_to_micro_post_id, ap_object_id, published_at, updated_at, deleted_at, version
FROM micro_posts
WHERE deleted_at IS NULL AND visibility = 'public'
ORDER BY published_at DESC
LIMIT $1 OFFSET $2`

	rows, err := m.DB.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	posts := make([]models.MicroPost, 0)
	for rows.Next() {
		var p models.MicroPost
		if err := rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Content,
			&p.Visibility,
			&p.ReplyToMicroPostID,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return posts, nil
}

// ListByActor returns a paginated list of micro posts for a specific actor.
func (m MicroPostModel) ListByActor(
	ctx context.Context,
	actorID int64,
	limit, offset int,
) ([]models.MicroPost, error) {
	query := `
SELECT id, actor_id, content, visibility, reply_to_micro_post_id, ap_object_id, published_at, updated_at, deleted_at, version
FROM micro_posts
WHERE deleted_at IS NULL AND actor_id = $1
ORDER BY published_at DESC
LIMIT $2 OFFSET $3`

	rows, err := m.DB.QueryContext(ctx, query, actorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	posts := make([]models.MicroPost, 0)
	for rows.Next() {
		var p models.MicroPost
		if err := rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Content,
			&p.Visibility,
			&p.ReplyToMicroPostID,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return posts, nil
}
