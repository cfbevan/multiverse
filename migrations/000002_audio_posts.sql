CREATE TABLE IF NOT EXISTS audio_posts (
    id bigserial PRIMARY KEY,
    actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    visibility text NOT NULL DEFAULT 'public',
    media_asset_id bigint NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    ap_object_id text UNIQUE,
    published_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_audio_posts_actor_published ON audio_posts(actor_id, published_at DESC);
