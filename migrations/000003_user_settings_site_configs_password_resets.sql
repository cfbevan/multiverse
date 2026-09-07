CREATE TABLE IF NOT EXISTS user_settings (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    theme_preset text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS site_configs (
    key text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT true,
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    token_hash text PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO site_configs (key, enabled, description)
VALUES
    ('signup_enabled', true, 'Allow new user signups'),
    ('password_reset_enabled', true, 'Allow users to request password reset emails'),
    ('federation_enabled', true, 'Allow federation delivery and inbox processing'),
    ('blog_enabled', true, 'Show the blog vertical and its feeds'),
    ('micro_blog_enabled', true, 'Show the micro-blog vertical and its feeds'),
    ('pictures_enabled', true, 'Show the pictures vertical'),
    ('videos_enabled', true, 'Show the videos vertical'),
    ('audio_enabled', true, 'Show the audio vertical')
ON CONFLICT (key) DO NOTHING;

CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_user_expires ON password_reset_tokens(user_id, expires_at DESC);
