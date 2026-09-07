package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/lib/pq"
)

// ActorModel persists ActivityPub actor records.
type ActorModel struct {
	DB *sql.DB
}

// InsertLocal inserts a new local actor into the database.
func (m ActorModel) InsertLocal(ctx context.Context, actor *models.Actor) error {
	query := `
INSERT INTO actors (user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true)
RETURNING id, created_at, updated_at`

	if err := m.DB.QueryRowContext(ctx, query,
		actor.UserID,
		actor.Handle,
		actor.Domain,
		actor.InboxURL,
		actor.OutboxURL,
		actor.FollowersURL,
		actor.FollowingURL,
		actor.PublicKeyID,
		actor.PublicKeyPEM,
		actor.PrivateKeyPEM,
	).Scan(&actor.ID, &actor.CreatedAt, &actor.UpdatedAt); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return models.ErrDuplicateHandle
		}

		return err
	}

	actor.IsLocal = true

	return nil
}

// GetByID loads an actor by its database ID.
func (m ActorModel) GetByID(ctx context.Context, id int64) (*models.Actor, error) {
	query := `
SELECT id, user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local, created_at, updated_at
FROM actors
WHERE id = $1`

	return m.getActorByQuery(ctx, query, id)
}

// GetByUserID loads the actor associated with a user account.
func (m ActorModel) GetByUserID(ctx context.Context, userID int64) (*models.Actor, error) {
	query := `
SELECT id, user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local, created_at, updated_at
FROM actors
WHERE user_id = $1`

	return m.getActorByQuery(ctx, query, userID)
}

// GetByHandleAndDomain loads an actor by its handle and domain.
func (m ActorModel) GetByHandleAndDomain(
	ctx context.Context,
	handle, domain string,
) (*models.Actor, error) {
	query := `
SELECT id, user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local, created_at, updated_at
FROM actors
WHERE handle = $1 AND domain = $2`

	return m.getActorByQuery(ctx, query, handle, domain)
}

// GetByPublicKeyID loads an actor by its public key ID.
func (m ActorModel) GetByPublicKeyID(ctx context.Context, keyID string) (*models.Actor, error) {
	query := `
SELECT id, user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local, created_at, updated_at
FROM actors
WHERE public_key_id = $1`

	return m.getActorByQuery(ctx, query, keyID)
}

// ListFollowerInboxes lists inbox URLs for users following an actor.
func (m ActorModel) ListFollowerInboxes(ctx context.Context, actorID int64) ([]string, error) {
	query := `
SELECT DISTINCT a.inbox_url
FROM follows f
JOIN actors a ON a.id = f.follower_actor_id
WHERE f.followed_actor_id = $1`

	rows, err := m.DB.QueryContext(ctx, query, actorID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	inboxes := make([]string, 0)
	for rows.Next() {
		var inbox string
		if err := rows.Scan(&inbox); err != nil {
			return nil, err
		}
		inboxes = append(inboxes, inbox)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return inboxes, nil
}

// CountFollowers returns the number of followers for an actor.
func (m ActorModel) CountFollowers(ctx context.Context, actorID int64) (int, error) {
	const query = `SELECT count(*) FROM follows WHERE followed_actor_id = $1`
	var n int
	if err := m.DB.QueryRowContext(ctx, query, actorID).Scan(&n); err != nil {
		return 0, err
	}

	return n, nil
}

// CountFollowing returns the number of actors followed by an actor.
func (m ActorModel) CountFollowing(ctx context.Context, actorID int64) (int, error) {
	const query = `SELECT count(*) FROM follows WHERE follower_actor_id = $1`
	var n int
	if err := m.DB.QueryRowContext(ctx, query, actorID).Scan(&n); err != nil {
		return 0, err
	}

	return n, nil
}

func (m ActorModel) getActorByQuery(
	ctx context.Context,
	query string,
	args ...any,
) (*models.Actor, error) {
	var actor models.Actor
	err := m.DB.QueryRowContext(ctx, query, args...).Scan(
		&actor.ID,
		&actor.UserID,
		&actor.Handle,
		&actor.Domain,
		&actor.InboxURL,
		&actor.OutboxURL,
		&actor.FollowersURL,
		&actor.FollowingURL,
		&actor.PublicKeyID,
		&actor.PublicKeyPEM,
		&actor.PrivateKeyPEM,
		&actor.IsLocal,
		&actor.CreatedAt,
		&actor.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &actor, nil
}
