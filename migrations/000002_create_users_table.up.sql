CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email CITEXT UNIQUE NOT NULL,
    password_hash BYTEA NOT NULL,
    fullname TEXT NOT NULL DEFAULT '',
    activated BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
    updated_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
    version INTEGER NOT NULL DEFAULT 1
);

CREATE TRIGGER set_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW
    EXECUTE FUNCTION trigger_set_updated_at();