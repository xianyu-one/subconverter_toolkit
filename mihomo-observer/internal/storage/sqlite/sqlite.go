package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mihomo-observer/internal/mihomo"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db    *sql.DB
	epoch int64
	zone  *time.Location
}

func Open(path, zone string) (*Store, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	if path == "" || path == ":memory:" {
		return nil, errors.New("persistent database path required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 2 {
		db.Close()
		return nil, fmt.Errorf("unsupported schema version %d", version)
	}
	if version == 0 {
		ddl, err := migrations.ReadFile("migrations/0001_init.sql")
		if err != nil {
			db.Close()
			return nil, err
		}
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err = tx.Exec(string(ddl)); err == nil {
			_, err = tx.Exec("PRAGMA user_version=1")
		}
		if err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
		version = 1
	}
	if version == 1 {
		ddl, readErr := migrations.ReadFile("migrations/0002_target_changes.sql")
		if readErr != nil {
			db.Close()
			return nil, readErr
		}
		tx, beginErr := db.Begin()
		if beginErr != nil {
			db.Close()
			return nil, beginErr
		}
		if _, err = tx.Exec(string(ddl)); err == nil {
			_, err = tx.Exec("PRAGMA user_version=2")
		}
		if err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
	}
	var existing string
	err = db.QueryRow("SELECT reporting_timezone FROM observer_settings WHERE id=1").Scan(&existing)
	if err == sql.ErrNoRows {
		_, err = db.Exec("INSERT INTO observer_settings(id,reporting_timezone) VALUES(1,?)", zone)
	} else if err == nil && existing != zone {
		err = fmt.Errorf("reporting_timezone cannot change for an existing database (%s)", existing)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	now := time.Now().UnixMilli()
	res, err := db.Exec("INSERT INTO observation_epochs(opened_at_ms,reason) VALUES(?,?)", now, "observer_start")
	if err != nil {
		db.Close()
		return nil, err
	}
	epoch, _ := res.LastInsertId()
	// A process restart breaks snapshot continuity even if Mihomo kept the same connections.
	_, err = db.Exec("UPDATE connections_raw SET observation_state='unknown',disappearance_seen_at_ms=NULL WHERE observation_state='active'")
	if err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.Exec("INSERT INTO collector_checkpoint(id) VALUES(1) ON CONFLICT(id) DO NOTHING")
	if err != nil {
		db.Close()
		return nil, err
	}
	var previous sql.NullInt64
	if err = db.QueryRow("SELECT last_committed_at_ms FROM collector_checkpoint WHERE id=1").Scan(&previous); err != nil {
		db.Close()
		return nil, err
	}
	if previous.Valid && previous.Int64 < now {
		_, err = db.Exec("INSERT INTO collection_gaps(stream,reason,started_at_ms,ended_at_ms) VALUES('observer','observer_restart',?,?)", previous.Int64, now)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, epoch: epoch, zone: loc}, nil
}
func (s *Store) Close() error {
	_, _ = s.db.Exec("UPDATE observation_epochs SET closed_at_ms=? WHERE id=?", time.Now().UnixMilli(), s.epoch)
	return s.db.Close()
}
func hour(ms int64) int64 { return ms - ms%3600000 }
func nullable[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}
func ptr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
func startPtr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
func key(epoch int64, c mihomo.Connection) string {
	b := fmt.Sprintf("%d\x00%s\x00%d", epoch, c.ID, func() int64 {
		if c.StartedAt != nil {
			return *c.StartedAt
		}
		return 0
	}())
	h := sha256.Sum256([]byte(b))
	return hex.EncodeToString(h[:])
}
func route(tx *sql.Tx, c mihomo.Connection) (int64, error) {
	chains, _ := json.Marshal(c.Chains)
	if c.Chains == nil {
		chains = []byte("[]")
	}
	providers, _ := json.Marshal(c.ProviderChains)
	if c.Rule == "" && c.RulePayload == "" && len(c.Chains) == 0 {
		return 1, nil
	}
	b, _ := json.Marshal([]any{c.Rule, c.RulePayload, c.Chains, c.ProviderChains})
	h := sha256.Sum256(b)
	rk := hex.EncodeToString(h[:])
	_, err := tx.Exec("INSERT INTO route_paths(route_key,rule,rule_payload,chains_json,provider_chains_json) VALUES(?,?,?,?,?) ON CONFLICT(route_key) DO NOTHING", rk, nullable(c.Rule), nullable(c.RulePayload), string(chains), nullable(string(providers)))
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRow("SELECT id FROM route_paths WHERE route_key=?", rk).Scan(&id)
	return id, err
}

type old struct {
	id                                       int64
	external                                 string
	started                                  sql.NullInt64
	upload, download, lastAt                 int64
	ip, host, sniff, targetKind, targetValue string
	route                                    int64
	state                                    string
}

func (s *Store) ApplyFrame(ctx context.Context, f mihomo.Frame, continuous bool) error {
	if len(f.Connections) > 100000 {
		return errors.New("frame connection limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seq int64
	var previousCommitted sql.NullInt64
	if err = tx.QueryRow("SELECT last_frame_seq,last_committed_at_ms FROM collector_checkpoint WHERE id=1").Scan(&seq, &previousCommitted); err != nil {
		return err
	}
	seq++
	rows, err := tx.QueryContext(ctx, "SELECT id,external_id,api_started_at_ms,upload_bytes,download_bytes,last_sample_at_ms,COALESCE(destination_ip,''),COALESCE(host,''),COALESCE(sniff_host,''),target_kind,COALESCE(target_value,''),route_id,observation_state FROM connections_raw WHERE epoch_id=? AND observation_state='active'", s.epoch)
	if err != nil {
		return err
	}
	prev := map[string]old{}
	for rows.Next() {
		var o old
		if err = rows.Scan(&o.id, &o.external, &o.started, &o.upload, &o.download, &o.lastAt, &o.ip, &o.host, &o.sniff, &o.targetKind, &o.targetValue, &o.route, &o.state); err != nil {
			rows.Close()
			return err
		}
		prev[o.external] = o
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range f.Connections {
		seen[c.ID] = true
		o, exists := prev[c.ID]
		conflict := exists && ((o.started.Valid && c.StartedAt != nil && o.started.Int64 != *c.StartedAt) || (o.ip != "" && c.Metadata.DestinationIP != "" && o.ip != c.Metadata.DestinationIP) || c.Upload < o.upload || c.Download < o.download)
		if conflict {
			_, err = tx.ExecContext(ctx, "UPDATE connections_raw SET observation_state='unknown' WHERE id=?", o.id)
			if err != nil {
				return err
			}
			exists = false
		}
		rid, e := route(tx, c)
		if e != nil {
			return e
		}
		if !exists {
			// Same ID with conflicting counters can coexist under a new generation.
			identity := fmt.Sprintf("%s-%d", key(s.epoch, c), seq)
			res, e := tx.ExecContext(ctx, `INSERT INTO connections_raw(identity_key,epoch_id,external_id,api_started_at_ms,first_seen_at_ms,last_seen_at_ms,observation_state,last_sample_at_ms,last_frame_seq,source_ip,source_port,destination_ip,destination_port,host,sniff_host,target_kind,target_value,target_source,destination_asn,ip_version,network,inbound_type,dns_mode,route_id,first_seen_upload_bytes,first_seen_download_bytes,upload_bytes,download_bytes)
			VALUES(?,?,?,?,?,?,'active',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, identity, s.epoch, c.ID, startPtr(c.StartedAt), f.ReceivedAt, f.ReceivedAt, f.ReceivedAt, seq, nil, ptr(c.Metadata.SourcePort.Value), nullable(c.Metadata.DestinationIP), ptr(c.Metadata.DestinationPort.Value), nullable(c.Metadata.Host), nullable(c.Metadata.SniffHost), c.TargetKind, nullable(c.TargetValue), c.TargetSource, nullable(c.Metadata.DestinationIPASN), c.IPVersion, c.Metadata.Network, nullable(c.Metadata.Type), nullable(c.Metadata.DNSMode), rid, c.Upload, c.Download, c.Upload, c.Download)
			if e != nil {
				return e
			}
			o.id, _ = res.LastInsertId()
			o.lastAt = f.ReceivedAt
			o.upload = c.Upload
			o.download = c.Download
		} else {
			_, e = tx.ExecContext(ctx, `UPDATE connections_raw SET last_seen_at_ms=?,last_sample_at_ms=?,last_frame_seq=?,sample_count=sample_count+1,destination_ip=COALESCE(destination_ip,?),destination_port=COALESCE(destination_port,?),host=COALESCE(NULLIF(?,''),host),sniff_host=COALESCE(NULLIF(?,''),sniff_host),target_kind=?,target_value=?,target_source=?,destination_asn=COALESCE(destination_asn,?),ip_version=CASE WHEN ip_version=0 THEN ? ELSE ip_version END,network=?,inbound_type=COALESCE(inbound_type,?),dns_mode=COALESCE(dns_mode,?),route_id=?,upload_bytes=?,download_bytes=? WHERE id=?`, f.ReceivedAt, f.ReceivedAt, seq, nullable(c.Metadata.DestinationIP), ptr(c.Metadata.DestinationPort.Value), c.Metadata.Host, c.Metadata.SniffHost, c.TargetKind, nullable(c.TargetValue), c.TargetSource, nullable(c.Metadata.DestinationIPASN), c.IPVersion, c.Metadata.Network, nullable(c.Metadata.Type), nullable(c.Metadata.DNSMode), rid, c.Upload, c.Download, o.id)
			if e != nil {
				return e
			}
		}
		du, dd, active := int64(0), int64(0), int64(0)
		if exists && continuous && f.ReceivedAt >= o.lastAt && f.ReceivedAt-o.lastAt <= 120000 {
			du = c.Upload - o.upload
			dd = c.Download - o.download
			active = f.ReceivedAt - o.lastAt
		}
		if exists && (o.targetKind != c.TargetKind || o.targetValue != c.TargetValue) && c.TargetKind == "domain" {
			_, e = tx.ExecContext(ctx, "INSERT INTO target_changes(connection_id,changed_at_ms,from_kind,from_value,to_kind,to_value,to_source) VALUES(?,?,?,?,?,?,?)", o.id, f.ReceivedAt, o.targetKind, nullable(o.targetValue), c.TargetKind, nullable(c.TargetValue), c.TargetSource)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "UPDATE connection_hours SET target_kind=?,target_value=? WHERE connection_id=?", c.TargetKind, c.TargetValue, o.id)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO projection_dirty(bucket_kind,bucket_start_ms,changed_at_ms) SELECT 'hour',bucket_start_ms,? FROM connection_hours WHERE connection_id=? ON CONFLICT(bucket_kind,bucket_start_ms) DO UPDATE SET revision=revision+1,changed_at_ms=excluded.changed_at_ms", f.ReceivedAt, o.id)
			if e != nil {
				return e
			}
		}
		if exists && (c.Metadata.DestinationIP != "" || c.Metadata.DestinationIPASN != "") {
			result, e := tx.ExecContext(ctx, `UPDATE connection_hours SET destination_ip=COALESCE(destination_ip,?),destination_asn=COALESCE(destination_asn,?) WHERE connection_id=? AND ((destination_ip IS NULL AND ?<>'') OR (destination_asn IS NULL AND ?<>''))`, nullable(c.Metadata.DestinationIP), nullable(c.Metadata.DestinationIPASN), o.id, c.Metadata.DestinationIP, c.Metadata.DestinationIPASN)
			if e != nil {
				return e
			}
			changed, _ := result.RowsAffected()
			if changed > 0 {
				_, e = tx.ExecContext(ctx, "INSERT INTO projection_dirty(bucket_kind,bucket_start_ms,changed_at_ms) SELECT 'hour',bucket_start_ms,? FROM connection_hours WHERE connection_id=? ON CONFLICT(bucket_kind,bucket_start_ms) DO UPDATE SET revision=revision+1,changed_at_ms=excluded.changed_at_ms", f.ReceivedAt, o.id)
				if e != nil {
					return e
				}
			}
		}
		bucket := hour(f.ReceivedAt)
		if exists && active > 0 && hour(o.lastAt) < bucket {
			priorActive := bucket - o.lastAt
			active -= priorActive
			_, e = tx.ExecContext(ctx, `UPDATE connection_hours SET last_seen_at_ms=?,observed_active_ms=observed_active_ms+? WHERE connection_id=? AND bucket_start_ms=? AND route_id=?`, bucket, priorActive, o.id, hour(o.lastAt), o.route)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO projection_dirty(bucket_kind,bucket_start_ms,changed_at_ms) VALUES('hour',?,?) ON CONFLICT(bucket_kind,bucket_start_ms) DO UPDATE SET revision=revision+1,changed_at_ms=excluded.changed_at_ms", hour(o.lastAt), f.ReceivedAt)
			if e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO connection_hours(connection_id,bucket_start_ms,route_id,ip_version,network,target_kind,target_value,destination_ip,destination_asn,first_seen_at_ms,last_seen_at_ms,sample_count,observed_active_ms,observed_upload_bytes,observed_download_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(connection_id,bucket_start_ms,route_id,ip_version,network) DO UPDATE SET last_seen_at_ms=excluded.last_seen_at_ms,sample_count=sample_count+1,observed_active_ms=observed_active_ms+excluded.observed_active_ms,observed_upload_bytes=observed_upload_bytes+excluded.observed_upload_bytes,observed_download_bytes=observed_download_bytes+excluded.observed_download_bytes,target_kind=excluded.target_kind,target_value=excluded.target_value,destination_ip=COALESCE(connection_hours.destination_ip,excluded.destination_ip),destination_asn=COALESCE(connection_hours.destination_asn,excluded.destination_asn)`, o.id, bucket, rid, c.IPVersion, c.Metadata.Network, c.TargetKind, nullable(c.TargetValue), nullable(c.Metadata.DestinationIP), nullable(c.Metadata.DestinationIPASN), f.ReceivedAt, f.ReceivedAt, 1, active, du, dd)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO projection_dirty(bucket_kind,bucket_start_ms,changed_at_ms) VALUES('hour',?,?) ON CONFLICT(bucket_kind,bucket_start_ms) DO UPDATE SET revision=revision+1,changed_at_ms=excluded.changed_at_ms", bucket, f.ReceivedAt)
		if e != nil {
			return e
		}
	}
	for id, o := range prev {
		if seen[id] {
			continue
		}
		state := "unknown"
		var at any
		if continuous {
			state = "absent"
			at = f.ReceivedAt
		}
		_, err = tx.ExecContext(ctx, "UPDATE connections_raw SET observation_state=?,disappearance_seen_at_ms=? WHERE id=?", state, at, o.id)
		if err != nil {
			return err
		}
	}
	if !continuous && previousCommitted.Valid {
		gapStart := f.ReceivedAt
		if previousCommitted.Int64 < f.ReceivedAt-1 {
			gapStart = previousCommitted.Int64 + 1
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO collection_gaps(stream,reason,started_at_ms,ended_at_ms) VALUES('connections','continuity_break',?,?)", gapStart, f.ReceivedAt)
		if err != nil {
			return err
		}
	}
	if f.UploadTotal != nil && f.DownloadTotal != nil {
		var oldUp, oldDown sql.NullInt64
		if err = tx.QueryRowContext(ctx, "SELECT traffic_upload_total,traffic_download_total FROM collector_checkpoint WHERE id=1").Scan(&oldUp, &oldDown); err != nil {
			return err
		}
		if continuous && oldUp.Valid && oldDown.Valid && *f.UploadTotal >= oldUp.Int64 && *f.DownloadTotal >= oldDown.Int64 {
			_, err = tx.ExecContext(ctx, `INSERT INTO global_traffic_hourly(bucket_start_ms,observed_upload_bytes,observed_download_bytes,sample_count,last_sample_at_ms) VALUES(?,?,?,?,?) ON CONFLICT(bucket_start_ms) DO UPDATE SET observed_upload_bytes=observed_upload_bytes+excluded.observed_upload_bytes,observed_download_bytes=observed_download_bytes+excluded.observed_download_bytes,sample_count=sample_count+1,last_sample_at_ms=excluded.last_sample_at_ms`, hour(f.ReceivedAt), *f.UploadTotal-oldUp.Int64, *f.DownloadTotal-oldDown.Int64, 1, f.ReceivedAt)
			if err != nil {
				return err
			}
			day := s.day(f.ReceivedAt)
			_, err = tx.ExecContext(ctx, `INSERT INTO global_traffic_daily(bucket_start_ms,observed_upload_bytes,observed_download_bytes,sample_count,last_sample_at_ms) VALUES(?,?,?,?,?) ON CONFLICT(bucket_start_ms) DO UPDATE SET observed_upload_bytes=observed_upload_bytes+excluded.observed_upload_bytes,observed_download_bytes=observed_download_bytes+excluded.observed_download_bytes,sample_count=sample_count+1,last_sample_at_ms=excluded.last_sample_at_ms`, day, *f.UploadTotal-oldUp.Int64, *f.DownloadTotal-oldDown.Int64, 1, f.ReceivedAt)
			if err != nil {
				return err
			}
		} else if continuous && oldUp.Valid && oldDown.Valid && (*f.UploadTotal < oldUp.Int64 || *f.DownloadTotal < oldDown.Int64) {
			_, err = tx.ExecContext(ctx, "INSERT INTO collection_gaps(stream,reason,started_at_ms,ended_at_ms) VALUES('traffic','counter_reset',?,?)", f.ReceivedAt, f.ReceivedAt)
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE collector_checkpoint SET traffic_upload_total=?,traffic_download_total=?,last_traffic_at_ms=? WHERE id=1", *f.UploadTotal, *f.DownloadTotal, f.ReceivedAt)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE collector_checkpoint SET last_committed_at_ms=?,last_connections_at_ms=?,last_frame_seq=? WHERE id=1", f.ReceivedAt, f.ReceivedAt, seq)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RecordGap(ctx context.Context, stream, reason string, start, end, dropped int64) error {
	if end < start {
		end = start
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO collection_gaps(stream,reason,started_at_ms,ended_at_ms,dropped_samples) VALUES(?,?,?,?,?)", stream, reason, start, end, dropped)
	return err
}
func (s *Store) SetControllerVersion(ctx context.Context, version string, meta bool) error {
	if len(version) > 200 {
		version = version[:200]
	}
	_, err := s.db.ExecContext(ctx, "UPDATE observation_epochs SET controller_version=?,controller_meta=? WHERE id=?", version, meta, s.epoch)
	return err
}
