CREATE TABLE organizations (
    id         uuid PRIMARY KEY,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Built-in roles have no org_id and fixed IDs. Their permissions come from code, so a module that
-- adds a permission does not need a data migration. Custom roles list theirs in role_permissions.
CREATE TABLE roles (
    id         uuid PRIMARY KEY,
    org_id     uuid REFERENCES organizations (id) ON DELETE CASCADE,
    key        text        NOT NULL CHECK (key IN ('owner', 'admin', 'member', 'custom')),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((org_id IS NULL) = (key <> 'custom'))
);
CREATE INDEX roles_org ON roles (org_id);
CREATE UNIQUE INDEX roles_org_name ON roles (org_id, lower(name));

INSERT INTO roles (id, org_id, key, name) VALUES
    ('00000000-0000-7000-8000-000000000001', NULL, 'owner', 'Owner'),
    ('00000000-0000-7000-8000-000000000002', NULL, 'admin', 'Admin'),
    ('00000000-0000-7000-8000-000000000003', NULL, 'member', 'Member');

CREATE TABLE role_permissions (
    role_id    uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission text NOT NULL,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE memberships (
    org_id     uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id    uuid        NOT NULL REFERENCES roles (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user ON memberships (user_id);
CREATE INDEX memberships_role ON memberships (role_id);

CREATE TABLE invitations (
    id          uuid PRIMARY KEY,
    org_id      uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email       text        NOT NULL,
    role_id     uuid        NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    token_hash  bytea       NOT NULL UNIQUE,
    invited_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invitations_org ON invitations (org_id, created_at);
