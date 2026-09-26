CREATE TABLE proxy_topology (
    at_ms INTEGER PRIMARY KEY,
    last_valid_ms INTEGER NOT NULL,
    fingerprint TEXT NOT NULL,
    topology_json TEXT NOT NULL,
    CHECK (last_valid_ms >= at_ms)
);
CREATE INDEX proxy_topology_last_valid_idx ON proxy_topology(last_valid_ms);
