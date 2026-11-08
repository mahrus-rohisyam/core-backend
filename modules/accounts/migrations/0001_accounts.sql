CREATE TABLE users (
    id                uuid PRIMARY KEY,
    email             text        NOT NULL,
    name              text        NOT NULL,
    password_hash     text,
    email_verified_at timestamptz,
    status            text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    is_instance_admin boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email ON users (lower(email));

-- id is shown in the session list; token_hash is what the cookie proves. The token itself is never stored.
CREATE TABLE sessions (
    id              uuid PRIMARY KEY,
    token_hash      bytea       NOT NULL UNIQUE,
    user_id         uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token      text        NOT NULL,
    mfa_verified_at timestamptz,
    ip              inet,
    user_agent      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);

CREATE TABLE totp_factors (
    user_id        uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret_sealed  bytea       NOT NULL,
    last_used_step bigint      NOT NULL DEFAULT 0,
    confirmed_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE recovery_codes (
    id        uuid PRIMARY KEY,
    user_id   uuid  NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash bytea NOT NULL,
    used_at   timestamptz
);
CREATE INDEX recovery_codes_user ON recovery_codes (user_id);

CREATE TABLE one_time_tokens (
    token_hash bytea PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX one_time_tokens_user ON one_time_tokens (user_id, purpose);
