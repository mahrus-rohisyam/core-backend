CREATE TABLE notes_notes (
    id         uuid PRIMARY KEY,
    org_id     uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    author_id  uuid        REFERENCES users (id) ON DELETE SET NULL,
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notes_notes_org ON notes_notes (org_id, id);
