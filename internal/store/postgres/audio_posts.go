package postgres

import (
	"context"
	"database/sql"

	"github.com/cfbevan/multiverse/internal/models"
)

// AudioPostModel persists audio post records.
type AudioPostModel struct {
	DB *sql.DB
}

// Insert stores a new audio post.
func (m AudioPostModel) Insert(ctx context.Context, post *models.AudioPost) error {
	query := `
INSERT INTO audio_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, published_at, updated_at, version`

	return m.DB.QueryRowContext(
		ctx,
		query,
		post.ActorID,
		post.Title,
		post.Description,
		post.Visibility,
		post.MediaAssetID,
		post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version)
}

// ListPublic lists public audio posts.
func (m AudioPostModel) ListPublic(
	ctx context.Context,
	limit, offset int,
) ([]models.AudioPost, error) {
	query := `
SELECT id, actor_id, title, description, visibility, media_asset_id, ap_object_id, published_at, updated_at, deleted_at, version
FROM audio_posts
WHERE deleted_at IS NULL AND visibility = 'public'
ORDER BY published_at DESC
LIMIT $1 OFFSET $2`

	rows, err := m.DB.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	posts := make([]models.AudioPost, 0)
	for rows.Next() {
		var p models.AudioPost
		if err := rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Title,
			&p.Description,
			&p.Visibility,
			&p.MediaAssetID,
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

// ListByActor lists audio posts for a single actor.
func (m AudioPostModel) ListByActor(
	ctx context.Context,
	actorID int64,
	limit, offset int,
) ([]models.AudioPost, error) {
	query := `
SELECT id, actor_id, title, description, visibility, media_asset_id, ap_object_id, published_at, updated_at, deleted_at, version
FROM audio_posts
WHERE deleted_at IS NULL AND actor_id = $1
ORDER BY published_at DESC
LIMIT $2 OFFSET $3`

	rows, err := m.DB.QueryContext(ctx, query, actorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	posts := make([]models.AudioPost, 0)
	for rows.Next() {
		var p models.AudioPost
		if err := rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Title,
			&p.Description,
			&p.Visibility,
			&p.MediaAssetID,
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
