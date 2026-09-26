-- Runtime migration 0001, derived from the reviewed Phase 2 schema.
-- Timestamps are UTC Unix milliseconds. Byte counters are nonnegative integers.

CREATE TABLE observer_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    reporting_timezone TEXT NOT NULL
);

CREATE TABLE observation_epochs (
    id INTEGER PRIMARY KEY,
    opened_at_ms INTEGER NOT NULL,
    closed_at_ms INTEGER,
    reason TEXT NOT NULL,
    controller_version TEXT,
    controller_meta INTEGER CHECK (controller_meta IN (0, 1)),
    CHECK (closed_at_ms IS NULL OR closed_at_ms >= opened_at_ms)
);

CREATE TABLE collector_checkpoint (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_committed_at_ms INTEGER,
    last_frame_seq INTEGER NOT NULL DEFAULT 0 CHECK (last_frame_seq >= 0),
    last_connections_at_ms INTEGER,
    last_traffic_at_ms INTEGER,
    traffic_upload_total INTEGER,
    traffic_download_total INTEGER
);

CREATE TABLE collection_gaps (
    id INTEGER PRIMARY KEY,
    stream TEXT NOT NULL CHECK (stream IN ('connections', 'traffic', 'writer', 'observer')),
    reason TEXT NOT NULL,
    started_at_ms INTEGER NOT NULL,
    ended_at_ms INTEGER,
    dropped_samples INTEGER NOT NULL DEFAULT 0 CHECK (dropped_samples >= 0),
    CHECK (ended_at_ms IS NULL OR ended_at_ms >= started_at_ms)
);

CREATE INDEX collection_gaps_time_idx ON collection_gaps(started_at_ms, ended_at_ms);

CREATE TABLE route_paths (
    id INTEGER PRIMARY KEY,
    route_key TEXT NOT NULL UNIQUE,
    rule TEXT,
    rule_payload TEXT,
    chains_json TEXT NOT NULL,
    provider_chains_json TEXT
);

-- 0 means "all paths" in aggregate rows; 1 means an unknown observed path.
INSERT INTO route_paths(id, route_key, chains_json) VALUES
    (0, '__all_paths__', '[]'),
    (1, '__unknown_path__', '[]');

CREATE TABLE connections_raw (
    id INTEGER PRIMARY KEY,
    identity_key TEXT NOT NULL UNIQUE,
    epoch_id INTEGER NOT NULL REFERENCES observation_epochs(id),
    external_id TEXT NOT NULL,
    api_started_at_ms INTEGER,
    first_seen_at_ms INTEGER NOT NULL,
    last_seen_at_ms INTEGER NOT NULL,
    disappearance_seen_at_ms INTEGER,
    observation_state TEXT NOT NULL CHECK (observation_state IN ('active', 'absent', 'unknown')),
    last_sample_at_ms INTEGER NOT NULL,
    last_frame_seq INTEGER NOT NULL CHECK (last_frame_seq >= 1),
    sample_count INTEGER NOT NULL DEFAULT 1 CHECK (sample_count >= 1),
    source_ip TEXT,
    source_port INTEGER CHECK (source_port IS NULL OR source_port BETWEEN 0 AND 65535),
    destination_ip TEXT,
    destination_port INTEGER CHECK (destination_port IS NULL OR destination_port BETWEEN 0 AND 65535),
    host TEXT,
    sniff_host TEXT,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('domain', 'ip_only', 'unknown')),
    target_value TEXT,
    target_source TEXT NOT NULL CHECK (target_source IN ('host', 'sniff_host', 'ip', 'unknown')),
    destination_asn TEXT,
    ip_version INTEGER NOT NULL CHECK (ip_version IN (0, 4, 6)),
    network TEXT NOT NULL,
    inbound_type TEXT,
    dns_mode TEXT,
    route_id INTEGER NOT NULL DEFAULT 1 REFERENCES route_paths(id) CHECK (route_id <> 0),
    first_seen_upload_bytes INTEGER NOT NULL DEFAULT 0 CHECK (first_seen_upload_bytes >= 0),
    first_seen_download_bytes INTEGER NOT NULL DEFAULT 0 CHECK (first_seen_download_bytes >= 0),
    upload_bytes INTEGER NOT NULL DEFAULT 0 CHECK (upload_bytes >= 0),
    download_bytes INTEGER NOT NULL DEFAULT 0 CHECK (download_bytes >= 0),
    process_name TEXT,
    process_path TEXT,
    CHECK (last_seen_at_ms >= first_seen_at_ms),
    CHECK (disappearance_seen_at_ms IS NULL OR disappearance_seen_at_ms >= last_seen_at_ms),
    CHECK ((target_kind = 'unknown' AND target_value IS NULL AND target_source = 'unknown') OR
           (target_kind = 'ip_only' AND target_value IS NOT NULL AND target_source = 'ip') OR
           (target_kind = 'domain' AND target_value IS NOT NULL AND target_source IN ('host', 'sniff_host'))),
    CHECK ((observation_state = 'absent' AND disappearance_seen_at_ms IS NOT NULL) OR
           (observation_state <> 'absent' AND disappearance_seen_at_ms IS NULL))
);

CREATE INDEX connections_raw_first_seen_idx ON connections_raw(first_seen_at_ms);
CREATE INDEX connections_raw_last_seen_idx ON connections_raw(last_seen_at_ms);
CREATE INDEX connections_raw_target_idx ON connections_raw(target_kind, target_value, last_seen_at_ms);
CREATE INDEX connections_raw_route_idx ON connections_raw(route_id, last_seen_at_ms);
CREATE INDEX connections_raw_active_idx ON connections_raw(observation_state, last_seen_at_ms);

-- One derived contribution per connection, hour, observed route, IP family and protocol.
-- A later host/sniffHost may reattribute target_kind/value within the raw retention window.
CREATE TABLE connection_hours (
    connection_id INTEGER NOT NULL REFERENCES connections_raw(id) ON DELETE CASCADE,
    bucket_start_ms INTEGER NOT NULL,
    route_id INTEGER NOT NULL REFERENCES route_paths(id) CHECK (route_id <> 0),
    ip_version INTEGER NOT NULL CHECK (ip_version IN (0, 4, 6)),
    network TEXT NOT NULL,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('domain', 'ip_only', 'unknown')),
    target_value TEXT,
    destination_ip TEXT,
    destination_asn TEXT,
    first_seen_at_ms INTEGER NOT NULL,
    last_seen_at_ms INTEGER NOT NULL,
    sample_count INTEGER NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
    observed_active_ms INTEGER NOT NULL DEFAULT 0 CHECK (observed_active_ms >= 0),
    observed_upload_bytes INTEGER NOT NULL DEFAULT 0 CHECK (observed_upload_bytes >= 0),
    observed_download_bytes INTEGER NOT NULL DEFAULT 0 CHECK (observed_download_bytes >= 0),
    PRIMARY KEY (connection_id, bucket_start_ms, route_id, ip_version, network),
    CHECK (last_seen_at_ms >= first_seen_at_ms),
    CHECK ((target_kind = 'unknown' AND target_value IS NULL) OR
           (target_kind <> 'unknown' AND target_value IS NOT NULL))
);

CREATE INDEX connection_hours_bucket_idx ON connection_hours(bucket_start_ms);
CREATE INDEX connection_hours_target_idx ON connection_hours(target_kind, target_value, bucket_start_ms);

-- Dirty buckets are set in the same transaction as raw/contribution updates.
CREATE TABLE projection_dirty (
    bucket_kind TEXT NOT NULL CHECK (bucket_kind IN ('hour', 'day')),
    bucket_start_ms INTEGER NOT NULL,
    changed_at_ms INTEGER NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    PRIMARY KEY (bucket_kind, bucket_start_ms)
);

CREATE TABLE analyzer_checkpoint (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_hour_end_ms INTEGER,
    last_day_end_ms INTEGER,
    last_success_at_ms INTEGER
);

-- subject_kind: domain, ip_only, destination_ip, asn, proxy_path.
-- route_id 0 is the all-path total; route_id 1 is an unknown observed path.
CREATE TABLE stats_hourly (
    bucket_start_ms INTEGER NOT NULL,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN
        ('domain', 'ip_only', 'destination_ip', 'asn', 'proxy_path')),
    subject_value TEXT NOT NULL,
    route_id INTEGER NOT NULL REFERENCES route_paths(id),
    ip_version INTEGER NOT NULL CHECK (ip_version IN (0, 4, 6)),
    network TEXT NOT NULL,
    observed_connections INTEGER NOT NULL CHECK (observed_connections >= 0),
    observed_upload_bytes INTEGER NOT NULL CHECK (observed_upload_bytes >= 0),
    observed_download_bytes INTEGER NOT NULL CHECK (observed_download_bytes >= 0),
    observed_active_ms INTEGER NOT NULL CHECK (observed_active_ms >= 0),
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    last_seen_at_ms INTEGER NOT NULL,
    PRIMARY KEY (bucket_start_ms, subject_kind, subject_value, route_id, ip_version, network)
);

CREATE INDEX stats_hourly_subject_idx ON stats_hourly(subject_kind, subject_value, bucket_start_ms DESC);
CREATE INDEX stats_hourly_route_idx ON stats_hourly(route_id, bucket_start_ms DESC);

CREATE TABLE stats_daily (
    bucket_start_ms INTEGER NOT NULL,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN
        ('domain', 'ip_only', 'destination_ip', 'asn', 'proxy_path')),
    subject_value TEXT NOT NULL,
    route_id INTEGER NOT NULL REFERENCES route_paths(id),
    ip_version INTEGER NOT NULL CHECK (ip_version IN (0, 4, 6)),
    network TEXT NOT NULL,
    observed_connections INTEGER NOT NULL CHECK (observed_connections >= 0),
    observed_upload_bytes INTEGER NOT NULL CHECK (observed_upload_bytes >= 0),
    observed_download_bytes INTEGER NOT NULL CHECK (observed_download_bytes >= 0),
    observed_active_ms INTEGER NOT NULL CHECK (observed_active_ms >= 0),
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    last_seen_at_ms INTEGER NOT NULL,
    PRIMARY KEY (bucket_start_ms, subject_kind, subject_value, route_id, ip_version, network)
);

CREATE INDEX stats_daily_subject_idx ON stats_daily(subject_kind, subject_value, bucket_start_ms DESC);
CREATE INDEX stats_daily_route_idx ON stats_daily(route_id, bucket_start_ms DESC);

CREATE TABLE global_traffic_hourly (
    bucket_start_ms INTEGER PRIMARY KEY,
    observed_upload_bytes INTEGER NOT NULL CHECK (observed_upload_bytes >= 0),
    observed_download_bytes INTEGER NOT NULL CHECK (observed_download_bytes >= 0),
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    last_sample_at_ms INTEGER NOT NULL
);

CREATE TABLE global_traffic_daily (
    bucket_start_ms INTEGER PRIMARY KEY,
    observed_upload_bytes INTEGER NOT NULL CHECK (observed_upload_bytes >= 0),
    observed_download_bytes INTEGER NOT NULL CHECK (observed_download_bytes >= 0),
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    last_sample_at_ms INTEGER NOT NULL
);

CREATE TABLE problem_records (
    id INTEGER PRIMARY KEY,
    fingerprint TEXT NOT NULL UNIQUE,
    problem_kind TEXT NOT NULL,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('domain', 'ip_only')),
    target_value TEXT NOT NULL,
    route_id INTEGER NOT NULL DEFAULT 0 REFERENCES route_paths(id),
    destination_ip TEXT,
    destination_port INTEGER CHECK (destination_port IS NULL OR destination_port BETWEEN 0 AND 65535),
    network TEXT,
    first_evidence_at_ms INTEGER NOT NULL,
    last_evidence_at_ms INTEGER NOT NULL,
    severity INTEGER NOT NULL CHECK (severity BETWEEN 1 AND 3),
    confidence INTEGER NOT NULL CHECK (confidence BETWEEN 1 AND 3),
    latest_sample_count INTEGER NOT NULL CHECK (latest_sample_count >= 0),
    latest_evidence_json TEXT NOT NULL,
    evidence_version INTEGER NOT NULL DEFAULT 1,
    CHECK (last_evidence_at_ms >= first_evidence_at_ms)
);

CREATE INDEX problem_records_recent_idx ON problem_records(last_evidence_at_ms DESC, severity DESC);
CREATE INDEX problem_records_target_idx ON problem_records(target_kind, target_value, last_evidence_at_ms DESC);

CREATE TABLE problem_occurrences (
    id INTEGER PRIMARY KEY,
    problem_id INTEGER NOT NULL REFERENCES problem_records(id) ON DELETE CASCADE,
    window_start_ms INTEGER NOT NULL,
    window_end_ms INTEGER NOT NULL,
    evidence_first_seen_ms INTEGER NOT NULL,
    evidence_last_seen_ms INTEGER NOT NULL,
    severity INTEGER NOT NULL CHECK (severity BETWEEN 1 AND 3),
    confidence INTEGER NOT NULL CHECK (confidence BETWEEN 1 AND 3),
    sample_count INTEGER NOT NULL CHECK (sample_count >= 0),
    evidence_json TEXT NOT NULL,
    evidence_version INTEGER NOT NULL DEFAULT 1,
    UNIQUE (problem_id, window_start_ms, window_end_ms),
    CHECK (window_end_ms > window_start_ms),
    CHECK (evidence_first_seen_ms <= evidence_last_seen_ms),
    CHECK (evidence_first_seen_ms >= window_start_ms AND evidence_last_seen_ms < window_end_ms)
);

CREATE INDEX problem_occurrences_time_idx ON problem_occurrences(window_start_ms DESC);
