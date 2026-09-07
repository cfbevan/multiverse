package postgres

import (
	"context"
	"database/sql"

	"github.com/cfbevan/multiverse/internal/models"
)

// BlogPostModel persists blog post records.
type BlogPostModel struct {
	DB *sql.DB
}

// Insert stores a new blog post.
func (m BlogPostModel) Insert(ctx context.Context, post *models.BlogPost) error {
	query := `
INSERT INTO blog_posts (actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, published_at, updated_at, version`

	return m.DB.QueryRowContext(
		ctx,
		query,
		post.ActorID,
		post.Title,
		post.Slug,
		post.BodyMarkdown,
		post.BodyHTML,
		post.Visibility,
		post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version)
}

func scanRows[T any](rows *sql.Rows, scan func(*sql.Rows, *T) error) ([]T, error) {
	items := make([]T, 0)
	for rows.Next() {
		var item T
		if err := scan(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func scanBlogPosts(rows *sql.Rows) ([]models.BlogPost, error) {
	return scanRows(rows, func(row *sql.Rows, p *models.BlogPost) error {
		return row.Scan(
			&p.ID,
			&p.ActorID,
			&p.Title,
			&p.Slug,
			&p.BodyMarkdown,
			&p.BodyHTML,
			&p.Visibility,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		)
	})
}

// ListPublic lists public blog posts.
func (m BlogPostModel) ListPublic(
	ctx context.Context,
	limit, offset int,
) ([]models.BlogPost, error) {
	query := `
SELECT id, actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id, published_at, updated_at, deleted_at, version
FROM blog_posts
WHERE deleted_at IS NULL AND visibility = 'public'
ORDER BY published_at DESC
LIMIT $1 OFFSET $2`

	rows, err := m.DB.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanBlogPosts(rows)
}

// ListByActor lists blog posts for a single actor.
func (m BlogPostModel) ListByActor(
	ctx context.Context,
	actorID int64,
	limit, offset int,
) ([]models.BlogPost, error) {
	query := `
SELECT id, actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id, published_at, updated_at, deleted_at, version
FROM blog_posts
WHERE deleted_at IS NULL AND actor_id = $1
ORDER BY published_at DESC
LIMIT $2 OFFSET $3`

	rows, err := m.DB.QueryContext(ctx, query, actorID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanBlogPosts(rows)
}
