CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id bigserial PRIMARY KEY,
    email text NOT NULL UNIQUE,
    handle text NOT NULL UNIQUE,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    bio text NOT NULL DEFAULT '',
    is_admin boolean NOT NULL DEFAULT false,
    activated boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS actors (
    id bigserial PRIMARY KEY,
    user_id bigint REFERENCES users(id) ON DELETE CASCADE,
    handle text NOT NULL UNIQUE,
    domain text NOT NULL,
    inbox_url text NOT NULL,
    outbox_url text NOT NULL,
    followers_url text NOT NULL,
    following_url text NOT NULL,
    public_key_id text,
    public_key_pem text,
    private_key_pem text,
    is_local boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (handle, domain)
);

CREATE TABLE IF NOT EXISTS follows (
    id bigserial PRIMARY KEY,
    follower_actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    followed_actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    ap_follow_activity_id text,
    approved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (follower_actor_id, followed_actor_id),
    UNIQUE (ap_follow_activity_id)
);

CREATE TABLE IF NOT EXISTS blog_posts (
    id bigserial PRIMARY KEY,
    actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    title text NOT NULL,
    slug text NOT NULL,
    body_markdown text NOT NULL,
    body_html text NOT NULL,
    visibility text NOT NULL DEFAULT 'public',
    ap_object_id text UNIQUE,
    published_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1,
    UNIQUE (actor_id, slug)
);

CREATE TABLE IF NOT EXISTS micro_posts (
    id bigserial PRIMARY KEY,
    actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    content text NOT NULL,
    visibility text NOT NULL DEFAULT 'public',
    reply_to_micro_post_id bigint REFERENCES micro_posts(id) ON DELETE SET NULL,
    ap_object_id text UNIQUE,
    published_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS media_assets (
    id bigserial PRIMARY KEY,
    owner_actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    bucket text NOT NULL,
    object_key text NOT NULL,
    media_type text NOT NULL,
    byte_size bigint NOT NULL,
    sha256_hex text NOT NULL,
    original_filename text NOT NULL,
    width integer,
    height integer,
    duration_seconds integer,
    is_public boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (bucket, object_key)
);

CREATE TABLE IF NOT EXISTS picture_posts (
    id bigserial PRIMARY KEY,
    actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    caption text NOT NULL DEFAULT '',
    visibility text NOT NULL DEFAULT 'public',
    ap_object_id text UNIQUE,
    published_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    version integer NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS picture_post_assets (
    picture_post_id bigint NOT NULL REFERENCES picture_posts(id) ON DELETE CASCADE,
    media_asset_id bigint NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    position integer NOT NULL DEFAULT 0,
    PRIMARY KEY (picture_post_id, media_asset_id)
);

CREATE TABLE IF NOT EXISTS video_posts (
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

CREATE TABLE IF NOT EXISTS activities (
    id bigserial PRIMARY KEY,
    actor_id bigint REFERENCES actors(id) ON DELETE SET NULL,
    activity_type text NOT NULL,
    object_type text NOT NULL,
    object_local_id bigint,
    object_ap_id text,
    ap_activity_id text UNIQUE,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS inbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id bigint REFERENCES actors(id) ON DELETE SET NULL,
    source_domain text NOT NULL,
    signature_key_id text,
    digest text,
    request_id text,
    idempotency_key text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    status text NOT NULL DEFAULT 'pending',
    error_message text,
    UNIQUE (idempotency_key)
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id bigint NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    target_inbox_url text NOT NULL,
    target_domain text NOT NULL,
    idempotency_key text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (idempotency_key)
);

CREATE TABLE IF NOT EXISTS token_revocations (
    id bigserial PRIMARY KEY,
    jti text NOT NULL UNIQUE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_actors_domain ON actors(domain);
CREATE INDEX IF NOT EXISTS idx_micro_posts_actor_published ON micro_posts(actor_id, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_blog_posts_actor_published ON blog_posts(actor_id, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_picture_posts_actor_published ON picture_posts(actor_id, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_video_posts_actor_published ON video_posts(actor_id, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_inbox_events_pending ON inbox_events(status, received_at);
CREATE INDEX IF NOT EXISTS idx_outbox_events_delivery ON outbox_events(status, next_attempt_at);
