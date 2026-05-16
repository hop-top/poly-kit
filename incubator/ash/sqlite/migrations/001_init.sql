-- 001_init: Create sessions and turns tables.
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    metadata   TEXT NOT NULL DEFAULT '{}',
    parent_id  TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at  TEXT
);

CREATE TABLE IF NOT EXISTS turns (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    parts      TEXT NOT NULL DEFAULT '[]',
    tool_calls TEXT NOT NULL DEFAULT '[]',
    parent_id  TEXT NOT NULL DEFAULT '',
    timestamp  TEXT NOT NULL,
    metadata   TEXT NOT NULL DEFAULT '{}',
    seq        INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_turns_session_seq
    ON turns(session_id, seq);

CREATE INDEX IF NOT EXISTS idx_sessions_parent
    ON sessions(parent_id);

CREATE INDEX IF NOT EXISTS idx_sessions_created
    ON sessions(created_at);
