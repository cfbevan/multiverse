CREATE TABLE IF NOT EXISTS comments (
    id bigserial PRIMARY KEY,
    entity_type text NOT NULL,
    entity_id bigint NOT NULL,
    actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    content text NOT NULL,
    reply_to_comment_id bigint REFERENCES comments(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_comments_entity ON comments(entity_type, entity_id, created_at DESC);

INSERT INTO site_configs (key, enabled, description)
VALUES
    ('oidc_enabled', true, 'Allow login through OpenID Connect providers'),
    ('oidc_login_enabled', true, 'Enable OIDC login buttons in the UI')
ON CONFLICT (key) DO UPDATE SET enabled = EXCLUDED.enabled, description = EXCLUDED.description, updated_at = now();
