CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY,
    org_id       uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    created_by   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    prefix       text        NOT NULL,
    token_hash   bytea       NOT NULL UNIQUE,
    permissions  text[]      NOT NULL,
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_tokens_org ON api_tokens (org_id, created_at);
