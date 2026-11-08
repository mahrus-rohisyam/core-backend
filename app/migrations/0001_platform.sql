-- Platform tables shared by every module.

CREATE TABLE rate_limits (
    key          text        NOT NULL,
    window_start timestamptz NOT NULL,
    count        integer     NOT NULL,
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (key, window_start)
);
CREATE INDEX rate_limits_expires_at ON rate_limits (expires_at);

-- No foreign keys: audit events outlive the users, tokens and organizations they mention.
CREATE TABLE audit_events (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_id         uuid,
    actor_user_id  uuid,
    actor_token_id uuid,
    action         text        NOT NULL,
    target_type    text        NOT NULL DEFAULT '',
    target_id      text        NOT NULL DEFAULT '',
    ip             inet,
    details        jsonb,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_org ON audit_events (org_id, id DESC);
CREATE INDEX audit_events_created_at ON audit_events (created_at);
