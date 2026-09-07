package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cfbevan/multiverse/internal/models"
)

// UserSettingsModel persists user preferences.
type UserSettingsModel struct {
	DB *sql.DB
}

// GetByUserID loads settings for a user.
func (m UserSettingsModel) GetByUserID(
	ctx context.Context,
	userID int64,
) (*models.UserSetting, error) {
	const query = `
SELECT user_id, theme_preset, created_at, updated_at
FROM user_settings
WHERE user_id = $1`

	var setting models.UserSetting
	err := m.DB.QueryRowContext(ctx, query, userID).Scan(
		&setting.UserID,
		&setting.ThemePreset,
		&setting.CreatedAt,
		&setting.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &setting, nil
}

// UpsertThemePreset stores the selected theme preset for a user.
func (m UserSettingsModel) UpsertThemePreset(
	ctx context.Context,
	userID int64,
	themePreset string,
) error {
	const query = `
INSERT INTO user_settings (user_id, theme_preset)
VALUES ($1, $2)
ON CONFLICT (user_id)
DO UPDATE SET theme_preset = EXCLUDED.theme_preset, updated_at = now()`

	_, err := m.DB.ExecContext(ctx, query, userID, themePreset)

	return err
}
