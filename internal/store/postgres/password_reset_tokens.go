package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
)

// PasswordResetTokenModel persists password reset token records.
type PasswordResetTokenModel struct {
	DB *sql.DB
}

// Insert stores a password reset token.
func (m PasswordResetTokenModel) Insert(
	ctx context.Context,
	token *models.PasswordResetToken,
) error {
	const query = `
INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
VALUES ($1, $2, $3)
RETURNING created_at`

	return m.DB.QueryRowContext(ctx, query, token.TokenHash, token.UserID, token.ExpiresAt).
		Scan(&token.CreatedAt)
}

// GetByTokenHash loads a password reset token by hash.
func (m PasswordResetTokenModel) GetByTokenHash(
	ctx context.Context,
	tokenHash string,
) (*models.PasswordResetToken, error) {
	const query = `
SELECT token_hash, user_id, expires_at, used_at, created_at
FROM password_reset_tokens
WHERE token_hash = $1`

	var token models.PasswordResetToken
	err := m.DB.QueryRowContext(ctx, query, tokenHash).Scan(
		&token.TokenHash,
		&token.UserID,
		&token.ExpiresAt,
		&token.UsedAt,
		&token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &token, nil
}

// MarkUsed marks a password reset token as consumed.
func (m PasswordResetTokenModel) MarkUsed(
	ctx context.Context,
	tokenHash string,
	usedAt time.Time,
) error {
	const query = `
UPDATE password_reset_tokens
SET used_at = $2
WHERE token_hash = $1`
	_, err := m.DB.ExecContext(ctx, query, tokenHash, usedAt)

	return err
}
