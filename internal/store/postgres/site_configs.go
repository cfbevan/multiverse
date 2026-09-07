package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/lib/pq"
)

// SiteConfigModel persists site configuration rows.
type SiteConfigModel struct {
	DB *sql.DB
}

// List loads all site configuration rows.
func (m SiteConfigModel) List(ctx context.Context) ([]models.SiteConfig, error) {
	const query = `
SELECT key, enabled, description, created_at, updated_at
FROM site_configs
ORDER BY key`

	rows, err := m.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	configs := make([]models.SiteConfig, 0)
	for rows.Next() {
		var config models.SiteConfig
		if err := rows.Scan(
			&config.Key,
			&config.Enabled,
			&config.Description,
			&config.CreatedAt,
			&config.UpdatedAt,
		); err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return configs, nil
}

// GetByKey loads a site config row by key.
func (m SiteConfigModel) GetByKey(ctx context.Context, key string) (*models.SiteConfig, error) {
	const query = `
SELECT key, enabled, description, created_at, updated_at
FROM site_configs
WHERE key = $1`

	var config models.SiteConfig
	err := m.DB.QueryRowContext(ctx, query, key).
		Scan(&config.Key, &config.Enabled, &config.Description, &config.CreatedAt, &config.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &config, nil
}

// UpdateEnabled toggles the enabled state of a config key.
func (m SiteConfigModel) UpdateEnabled(ctx context.Context, key string, enabled bool) error {
	const query = `
UPDATE site_configs
SET enabled = $2, updated_at = now()
WHERE key = $1`
	result, err := m.DB.ExecContext(ctx, query, key, enabled)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return models.ErrRecordNotFound
	}

	return nil
}

// Upsert inserts or updates a site config row.
func (m SiteConfigModel) Upsert(ctx context.Context, config *models.SiteConfig) error {
	const query = `
INSERT INTO site_configs (key, enabled, description)
VALUES ($1, $2, $3)
ON CONFLICT (key)
DO UPDATE SET enabled = EXCLUDED.enabled, description = EXCLUDED.description, updated_at = now()
RETURNING created_at, updated_at`

	return m.DB.QueryRowContext(ctx, query, config.Key, config.Enabled, config.Description).
		Scan(&config.CreatedAt, &config.UpdatedAt)
}

// InsertDefaults upserts a set of default site configs.
func (m SiteConfigModel) InsertDefaults(ctx context.Context, configs []models.SiteConfig) error {
	for i := range configs {
		if err := m.Upsert(ctx, &configs[i]); err != nil {
			if _, ok := errors.AsType[*pq.Error](err); ok {
				return err
			}

			return err
		}
	}

	return nil
}
