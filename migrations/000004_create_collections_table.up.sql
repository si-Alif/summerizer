CREATE TABLE IF NOT EXISTS collections (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    max_sources INTEGER NOT NULL DEFAULT 20,
    created_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
    version INTEGER NOT NULL DEFAULT 1,

    UNIQUE(user_id, title)
);

CREATE INDEX idx_collections_user_id ON collections(user_id);

CREATE TRIGGER set_collections_updated_at
    BEFORE UPDATE ON collections
    FOR EACH ROW
    EXECUTE FUNCTION trigger_set_updated_at();