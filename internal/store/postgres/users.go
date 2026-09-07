package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/lib/pq"
)

// UserModel persists user records.
type UserModel struct {
	DB *sql.DB
}

// Insert stores a new user record.
func (m UserModel) Insert(ctx context.Context, user *models.User) error {
	query := `
INSERT INTO users (email, handle, display_name, password_hash, bio, activated)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, created_at, updated_at, version`

	args := []any{
		user.Email,
		user.Handle,
		user.DisplayName,
		user.PasswordHash,
		user.Bio,
		user.Activated,
	}
	if err := m.DB.QueryRowContext(ctx, query, args...).
		Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt, &user.Version); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			switch pqErr.Constraint {
			case "users_email_key":
				return models.ErrDuplicateEmail
			case "users_handle_key":
				return models.ErrDuplicateHandle
			default:
				return models.ErrEditConflict
			}
		}

		return err
	}

	return nil
}

// GetByID loads a user by database ID.
func (m UserModel) GetByID(ctx context.Context, id int64) (*models.User, error) {
	query := `
SELECT id, email, handle, display_name, password_hash, bio, is_admin, activated, created_at, updated_at, version
FROM users
WHERE id = $1`

	return m.getUserByQuery(ctx, query, id)
}

// GetByEmail loads a user by email address.
func (m UserModel) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	query := `
SELECT id, email, handle, display_name, password_hash, bio, is_admin, activated, created_at, updated_at, version
FROM users
WHERE email = $1`

	return m.getUserByQuery(ctx, query, email)
}

// GetByHandle loads a user by handle.
func (m UserModel) GetByHandle(ctx context.Context, handle string) (*models.User, error) {
	query := `
SELECT id, email, handle, display_name, password_hash, bio, is_admin, activated, created_at, updated_at, version
FROM users
WHERE handle = $1`

	return m.getUserByQuery(ctx, query, handle)
}

// UpdatePasswordHash changes a user's password hash.
func (m UserModel) UpdatePasswordHash(
	ctx context.Context,
	userID int64,
	passwordHash string,
) error {
	const query = `
UPDATE users
SET password_hash = $2, updated_at = now(), version = version + 1
WHERE id = $1`
	_, err := m.DB.ExecContext(ctx, query, userID, passwordHash)

	return err
}

func (m UserModel) getUserByQuery(
	ctx context.Context,
	query string,
	args ...any,
) (*models.User, error) {
	var user models.User
	err := m.DB.QueryRowContext(ctx, query, args...).Scan(
		&user.ID,
		&user.Email,
		&user.Handle,
		&user.DisplayName,
		&user.PasswordHash,
		&user.Bio,
		&user.IsAdmin,
		&user.Activated,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.Version,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &user, nil
}
