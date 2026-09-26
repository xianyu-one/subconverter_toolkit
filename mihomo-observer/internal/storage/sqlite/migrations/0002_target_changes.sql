CREATE TABLE target_changes (
    id INTEGER PRIMARY KEY,
    connection_id INTEGER NOT NULL REFERENCES connections_raw(id) ON DELETE CASCADE,
    changed_at_ms INTEGER NOT NULL,
    from_kind TEXT NOT NULL,
    from_value TEXT,
    to_kind TEXT NOT NULL,
    to_value TEXT,
    to_source TEXT NOT NULL
);
CREATE INDEX target_changes_to_idx ON target_changes(to_kind,to_value,changed_at_ms DESC);
CREATE INDEX target_changes_from_idx ON target_changes(from_kind,from_value,changed_at_ms DESC);
