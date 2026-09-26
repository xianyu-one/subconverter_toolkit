package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type contribution struct {
	connection, bucket, route       int64
	family                          int
	network, kind, value, ip, asn   string
	up, down, active, samples, last int64
}
type aggregateKey struct {
	kind, value string
	route       int64
	family      int
	network     string
}
type aggregate struct {
	connections                     map[int64]bool
	up, down, active, samples, last int64
}

func (s *Store) day(ms int64) int64 {
	t := time.UnixMilli(ms).In(s.zone)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.zone).UnixMilli()
}
func (s *Store) nextDay(ms int64) int64 {
	return time.UnixMilli(ms).In(s.zone).AddDate(0, 0, 1).UnixMilli()
}

func (s *Store) rebuild(ctx context.Context, tx *sql.Tx, table string, start, end int64) error {
	if table != "stats_hourly" && table != "stats_daily" {
		return fmt.Errorf("invalid projection table")
	}
	rows, err := tx.QueryContext(ctx, "SELECT connection_id,bucket_start_ms,route_id,ip_version,network,target_kind,COALESCE(target_value,''),COALESCE(destination_ip,''),COALESCE(destination_asn,''),observed_upload_bytes,observed_download_bytes,observed_active_ms,sample_count,last_seen_at_ms FROM connection_hours WHERE bucket_start_ms>=? AND bucket_start_ms<?", start, end)
	if err != nil {
		return err
	}
	defer rows.Close()
	aggs := map[aggregateKey]*aggregate{}
	add := func(k aggregateKey, c contribution) {
		a := aggs[k]
		if a == nil {
			a = &aggregate{connections: map[int64]bool{}}
			aggs[k] = a
		}
		a.connections[c.connection] = true
		a.up += c.up
		a.down += c.down
		a.active += c.active
		a.samples += c.samples
		if c.last > a.last {
			a.last = c.last
		}
	}
	for rows.Next() {
		var c contribution
		if err = rows.Scan(&c.connection, &c.bucket, &c.route, &c.family, &c.network, &c.kind, &c.value, &c.ip, &c.asn, &c.up, &c.down, &c.active, &c.samples, &c.last); err != nil {
			return err
		}
		for _, rid := range []int64{0, c.route} {
			if c.value != "" {
				add(aggregateKey{c.kind, c.value, rid, c.family, c.network}, c)
			}
			if c.ip != "" {
				add(aggregateKey{"destination_ip", c.ip, rid, c.family, c.network}, c)
			}
			if c.asn != "" {
				add(aggregateKey{"asn", c.asn, rid, c.family, c.network}, c)
			}
		}
		add(aggregateKey{"proxy_path", fmt.Sprint(c.route), c.route, c.family, c.network}, c)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE bucket_start_ms=?", start); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO "+table+"(bucket_start_ms,subject_kind,subject_value,route_id,ip_version,network,observed_connections,observed_upload_bytes,observed_download_bytes,observed_active_ms,sample_count,last_seen_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, a := range aggs {
		if _, err = stmt.ExecContext(ctx, start, k.kind, k.value, k.route, k.family, k.network, len(a.connections), a.up, a.down, a.active, a.samples, a.last); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Project(ctx context.Context, now int64) error {
	rows, err := s.db.QueryContext(ctx, "SELECT bucket_start_ms,revision FROM projection_dirty WHERE bucket_kind='hour' ORDER BY bucket_start_ms LIMIT 24")
	if err != nil {
		return err
	}
	type dirty struct{ bucket, rev int64 }
	var work []dirty
	for rows.Next() {
		var d dirty
		if err = rows.Scan(&d.bucket, &d.rev); err != nil {
			rows.Close()
			return err
		}
		work = append(work, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range work {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err = s.rebuild(ctx, tx, "stats_hourly", d.bucket, d.bucket+3600000); err == nil {
			day := s.day(d.bucket)
			err = s.rebuild(ctx, tx, "stats_daily", day, s.nextDay(day))
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM projection_dirty WHERE bucket_kind='hour' AND bucket_start_ms=? AND revision=?", d.bucket, d.rev)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO analyzer_checkpoint(id,last_success_at_ms) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET last_success_at_ms=excluded.last_success_at_ms", now)
	return err
}
func (s *Store) Cleanup(ctx context.Context, now int64, rawDays, hourlyDays, dailyDays int) error {
	cut := now - int64(rawDays)*86400000
	for i := 0; i < 10; i++ {
		r, err := s.db.ExecContext(ctx, "DELETE FROM connections_raw WHERE id IN (SELECT id FROM connections_raw WHERE last_seen_at_ms<? AND observation_state<>'active' LIMIT 500)", cut)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n < 500 {
			break
		}
	}
	if hourlyDays > 0 {
		_, err := s.db.ExecContext(ctx, "DELETE FROM stats_hourly WHERE bucket_start_ms<?", now-int64(hourlyDays)*86400000)
		if err != nil {
			return err
		}
	}
	if dailyDays > 0 {
		_, err := s.db.ExecContext(ctx, "DELETE FROM stats_daily WHERE bucket_start_ms<?", now-int64(dailyDays)*86400000)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Dashboard(ctx context.Context, now int64) (map[string]any, error) {
	start := s.day(now)
	out := map[string]any{"as_of_ms": now, "today_start_ms": start}
	var last sql.NullInt64
	var current int
	err := s.db.QueryRowContext(ctx, "SELECT last_connections_at_ms FROM collector_checkpoint WHERE id=1").Scan(&last)
	if err != nil {
		return nil, err
	}
	if last.Valid {
		out["last_connections_at_ms"] = last.Int64
		var opened int64
		if err = s.db.QueryRowContext(ctx, "SELECT opened_at_ms FROM observation_epochs WHERE id=?", s.epoch).Scan(&opened); err != nil {
			return nil, err
		}
		if last.Int64 >= opened && now-last.Int64 < 10000 {
			err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM connections_raw WHERE observation_state='active' AND epoch_id=?", s.epoch).Scan(&current)
			if err != nil {
				return nil, err
			}
			out["current_connections"] = current
		}
	}
	var count int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM connections_raw WHERE first_seen_at_ms>=?", start).Scan(&count)
	if err != nil {
		return nil, err
	}
	out["today_observed_connections"] = count
	var up, down int64
	err = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(observed_upload_bytes),0),COALESCE(SUM(observed_download_bytes),0) FROM global_traffic_daily WHERE bucket_start_ms=?", start).Scan(&up, &down)
	if err != nil {
		return nil, err
	}
	out["today_observed_upload_bytes"] = up
	out["today_observed_download_bytes"] = down
	var problems int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM problem_records WHERE last_evidence_at_ms>=?", now-7*86400000).Scan(&problems)
	if err != nil {
		return nil, err
	}
	out["recent_problems"] = problems
	var domains, ips, ipv6 int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT CASE WHEN target_kind='domain' THEN target_value END),COUNT(DISTINCT CASE WHEN destination_ip IS NOT NULL THEN destination_ip WHEN target_kind='ip_only' THEN target_value END),COUNT(DISTINCT CASE WHEN problem_kind='ipv6_observation_difference' THEN target_value END) FROM problem_records WHERE last_evidence_at_ms>=?", now-7*86400000).Scan(&domains, &ips, &ipv6)
	if err != nil {
		return nil, err
	}
	out["anomalous_domains"] = domains
	out["anomalous_ips"] = ips
	out["ipv6_observation_differences"] = ipv6
	var asns int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT ch.destination_asn) FROM problem_records p JOIN connection_hours ch ON ch.target_kind=p.target_kind AND ch.target_value=p.target_value AND ch.bucket_start_ms>=? WHERE p.last_evidence_at_ms>=? AND ch.destination_asn IS NOT NULL`, hour(now-7*86400000), now-7*86400000).Scan(&asns)
	if err != nil {
		return nil, err
	}
	out["associated_asns"] = asns
	var gaps int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM collection_gaps WHERE started_at_ms>=?", now-86400000).Scan(&gaps)
	if err != nil {
		return nil, err
	}
	out["gaps_24h"] = gaps
	gapRows, err := s.db.QueryContext(ctx, "SELECT stream,reason,started_at_ms,ended_at_ms,dropped_samples FROM collection_gaps WHERE started_at_ms>=? ORDER BY started_at_ms DESC LIMIT 12", now-7*86400000)
	if err != nil {
		return nil, err
	}
	gapList := make([]map[string]any, 0)
	for gapRows.Next() {
		var stream, reason string
		var started, dropped int64
		var ended sql.NullInt64
		if err = gapRows.Scan(&stream, &reason, &started, &ended, &dropped); err != nil {
			gapRows.Close()
			return nil, err
		}
		entry := map[string]any{"stream": stream, "reason": reason, "started_at_ms": started, "dropped_samples": dropped}
		if ended.Valid {
			entry["ended_at_ms"] = ended.Int64
		}
		gapList = append(gapList, entry)
	}
	err = gapRows.Err()
	gapRows.Close()
	if err != nil {
		return nil, err
	}
	out["recent_gaps"] = gapList
	return out, nil
}
func (s *Store) Targets(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT subject_kind,subject_value,SUM(observed_connections),SUM(observed_upload_bytes),SUM(observed_download_bytes),MAX(last_seen_at_ms) FROM stats_daily WHERE route_id=0 AND subject_kind IN ('domain','ip_only','destination_ip') GROUP BY subject_kind,subject_value ORDER BY MAX(last_seen_at_ms) DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var kind, value string
		var count, up, down, last int64
		if err = rows.Scan(&kind, &value, &count, &up, &down, &last); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"kind": kind, "value": value, "observed_connections": count, "observed_upload_bytes": up, "observed_download_bytes": down, "last_seen_at_ms": last})
	}
	return out, rows.Err()
}
func (s *Store) Problems(ctx context.Context, now int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.problem_kind,p.target_kind,p.target_value,p.severity,p.confidence,p.latest_sample_count,p.latest_evidence_json,p.last_evidence_at_ms,r.rule,r.rule_payload,r.chains_json FROM problem_records p JOIN route_paths r ON r.id=p.route_id WHERE p.last_evidence_at_ms>=? ORDER BY p.severity DESC,p.confidence DESC,p.last_evidence_at_ms DESC LIMIT 200`, now-7*86400000)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var kind, tk, tv, evidence, chains string
		var sev, conf, n int
		var last int64
		var rule, payload sql.NullString
		if err = rows.Scan(&kind, &tk, &tv, &sev, &conf, &n, &evidence, &last, &rule, &payload, &chains); err != nil {
			return nil, err
		}
		var ev any
		_ = json.Unmarshal([]byte(evidence), &ev)
		nextCheck := "Review this target's observed rule, proxy chain and destination details before changing the subscription"
		if kind == "ipv6_observation_difference" {
			nextCheck = "Inspect IPv6 reachability, DNS AAAA and the observed rule; verify any fallback outside Observer"
		}
		out = append(out, map[string]any{"kind": kind, "target_kind": tk, "target": tv, "severity": sev, "confidence": conf, "sample_count": n, "evidence": ev, "next_check": nextCheck, "last_evidence_at_ms": last, "rule": rule.String, "rule_payload": payload.String, "chains_json": chains})
	}
	return out, rows.Err()
}
func (s *Store) Detail(ctx context.Context, kind, value string) (map[string]any, error) {
	if kind != "domain" && kind != "ip_only" && kind != "proxy_path" && kind != "destination_ip" && kind != "asn" {
		return nil, fmt.Errorf("unsupported detail kind")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT bucket_start_ms,route_id,ip_version,network,observed_connections,observed_upload_bytes,observed_download_bytes,observed_active_ms,sample_count,last_seen_at_ms FROM stats_hourly WHERE subject_kind=? AND subject_value=? AND bucket_start_ms>=? ORDER BY bucket_start_ms DESC LIMIT 500", kind, value, time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := make([]map[string]any, 0)
	for rows.Next() {
		var bucket, route, connections, up, down, active, samples, last int64
		var family int
		var network string
		if err = rows.Scan(&bucket, &route, &family, &network, &connections, &up, &down, &active, &samples, &last); err != nil {
			return nil, err
		}
		stats = append(stats, map[string]any{"bucket_start_ms": bucket, "route_id": route, "ip_version": family, "network": network, "observed_connections": connections, "observed_upload_bytes": up, "observed_download_bytes": down, "observed_active_ms": active, "sample_count": samples, "last_seen_at_ms": last})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]any{"kind": kind, "value": value, "hourly": stats}
	days, err := s.DailyHistory(ctx, kind, value, 1<<63-1)
	if err != nil {
		return nil, err
	}
	out["daily"] = days
	if len(days) > 0 && (kind == "domain" || kind == "ip_only" || kind == "proxy_path") {
		oldest := days[len(days)-1]["bucket_start_ms"].(int64)
		newest := days[0]["bucket_start_ms"].(int64)
		history, historyErr := s.ProblemHistory(ctx, kind, value, oldest, s.nextDay(newest))
		if historyErr != nil {
			return nil, historyErr
		}
		out["problem_history"] = history
	}
	routes, err := s.db.QueryContext(ctx, `SELECT DISTINCT r.id,COALESCE(r.rule,''),COALESCE(r.rule_payload,''),r.chains_json FROM route_paths r JOIN stats_daily h ON h.route_id=r.id WHERE h.subject_kind=? AND h.subject_value=? AND r.id<>0 ORDER BY r.id LIMIT 100`, kind, value)
	if err != nil {
		return nil, err
	}
	paths := make([]map[string]any, 0)
	for routes.Next() {
		var id int64
		var rule, payload, chains string
		if err = routes.Scan(&id, &rule, &payload, &chains); err != nil {
			routes.Close()
			return nil, err
		}
		var chainValues any
		_ = json.Unmarshal([]byte(chains), &chainValues)
		paths = append(paths, map[string]any{"id": id, "rule": rule, "rule_payload": payload, "chains": chainValues})
	}
	err = routes.Err()
	routes.Close()
	if err != nil {
		return nil, err
	}
	out["paths"] = paths
	var raw *sql.Rows
	if kind == "proxy_path" {
		raw, err = s.db.QueryContext(ctx, `SELECT c.target_kind,COALESCE(c.target_value,''),c.target_source,COALESCE(c.destination_ip,''),COALESCE(c.destination_asn,''),c.observation_state,c.first_seen_at_ms,c.last_seen_at_ms,c.route_id FROM connections_raw c WHERE c.route_id=? ORDER BY c.last_seen_at_ms DESC LIMIT 30`, value)
	} else if kind == "destination_ip" {
		raw, err = s.db.QueryContext(ctx, `SELECT c.target_kind,COALESCE(c.target_value,''),c.target_source,COALESCE(c.destination_ip,''),COALESCE(c.destination_asn,''),c.observation_state,c.first_seen_at_ms,c.last_seen_at_ms,c.route_id FROM connections_raw c WHERE c.destination_ip=? ORDER BY c.last_seen_at_ms DESC LIMIT 30`, value)
	} else if kind == "asn" {
		raw, err = s.db.QueryContext(ctx, `SELECT c.target_kind,COALESCE(c.target_value,''),c.target_source,COALESCE(c.destination_ip,''),COALESCE(c.destination_asn,''),c.observation_state,c.first_seen_at_ms,c.last_seen_at_ms,c.route_id FROM connections_raw c WHERE c.destination_asn=? ORDER BY c.last_seen_at_ms DESC LIMIT 30`, value)
	} else {
		raw, err = s.db.QueryContext(ctx, `SELECT c.target_kind,COALESCE(c.target_value,''),c.target_source,COALESCE(c.destination_ip,''),COALESCE(c.destination_asn,''),c.observation_state,c.first_seen_at_ms,c.last_seen_at_ms,c.route_id FROM connections_raw c WHERE c.target_kind=? AND c.target_value=? ORDER BY c.last_seen_at_ms DESC LIMIT 30`, kind, value)
	}
	if err != nil {
		return nil, err
	}
	samples := make([]map[string]any, 0)
	for raw.Next() {
		var targetKind, target, source, ip, asn, state string
		var first, last, route int64
		if err = raw.Scan(&targetKind, &target, &source, &ip, &asn, &state, &first, &last, &route); err != nil {
			raw.Close()
			return nil, err
		}
		samples = append(samples, map[string]any{"target_kind": targetKind, "target": target, "target_source": source, "destination_ip": ip, "destination_asn": asn, "state": state, "first_seen_at_ms": first, "last_seen_at_ms": last, "route_id": route})
	}
	err = raw.Err()
	raw.Close()
	if err != nil {
		return nil, err
	}
	out["recent_samples"] = samples
	if kind == "domain" {
		sourceRows, sourceErr := s.db.QueryContext(ctx, `SELECT target_source,COUNT(*),MIN(first_seen_at_ms),MAX(last_seen_at_ms) FROM connections_raw WHERE target_kind='domain' AND target_value=? GROUP BY target_source ORDER BY target_source`, value)
		if sourceErr != nil {
			return nil, sourceErr
		}
		sources := make([]map[string]any, 0)
		for sourceRows.Next() {
			var source string
			var count, first, last int64
			if sourceErr = sourceRows.Scan(&source, &count, &first, &last); sourceErr != nil {
				sourceRows.Close()
				return nil, sourceErr
			}
			sources = append(sources, map[string]any{"source": source, "observed_connections": count, "first_seen_at_ms": first, "last_seen_at_ms": last})
		}
		sourceErr = sourceRows.Err()
		sourceRows.Close()
		if sourceErr != nil {
			return nil, sourceErr
		}
		out["observed_sources"] = sources
	}
	if kind == "domain" || kind == "ip_only" || kind == "destination_ip" {
		var changes *sql.Rows
		if kind == "destination_ip" {
			changes, err = s.db.QueryContext(ctx, `SELECT t.changed_at_ms,t.from_kind,COALESCE(t.from_value,''),t.to_kind,COALESCE(t.to_value,''),t.to_source FROM target_changes t JOIN connections_raw c ON c.id=t.connection_id WHERE c.destination_ip=? ORDER BY t.changed_at_ms DESC LIMIT 20`, value)
		} else {
			changes, err = s.db.QueryContext(ctx, `SELECT changed_at_ms,from_kind,COALESCE(from_value,''),to_kind,COALESCE(to_value,''),to_source FROM target_changes WHERE (to_kind=? AND to_value=?) OR (from_kind=? AND from_value=?) ORDER BY changed_at_ms DESC LIMIT 20`, kind, value, kind, value)
		}
		if err != nil {
			return nil, err
		}
		transitions := make([]map[string]any, 0)
		for changes.Next() {
			var at int64
			var fromKind, fromValue, toKind, toValue, source string
			if err = changes.Scan(&at, &fromKind, &fromValue, &toKind, &toValue, &source); err != nil {
				changes.Close()
				return nil, err
			}
			transitions = append(transitions, map[string]any{"changed_at_ms": at, "from_kind": fromKind, "from_value": fromValue, "to_kind": toKind, "to_value": toValue, "to_source": source})
		}
		err = changes.Err()
		changes.Close()
		if err != nil {
			return nil, err
		}
		out["grouping_changes"] = transitions
	}
	if kind == "proxy_path" {
		carried, e := s.db.QueryContext(ctx, `SELECT subject_kind,subject_value,SUM(observed_connections),SUM(observed_upload_bytes+observed_download_bytes) FROM stats_daily WHERE route_id=? AND subject_kind IN ('domain','ip_only') GROUP BY subject_kind,subject_value ORDER BY SUM(observed_connections) DESC LIMIT 30`, value)
		if e != nil {
			return nil, e
		}
		targets := make([]map[string]any, 0)
		for carried.Next() {
			var tk, tv string
			var count, total int64
			if e = carried.Scan(&tk, &tv, &count, &total); e != nil {
				carried.Close()
				return nil, e
			}
			targets = append(targets, map[string]any{"kind": tk, "value": tv, "observed_connections": count, "observed_bytes": total})
		}
		e = carried.Err()
		carried.Close()
		if e != nil {
			return nil, e
		}
		out["carried_targets"] = targets
	}
	if kind == "domain" || kind == "ip_only" || kind == "proxy_path" {
		var problems *sql.Rows
		var e error
		if kind == "proxy_path" {
			problems, e = s.db.QueryContext(ctx, `SELECT problem_kind,severity,confidence,latest_sample_count,latest_evidence_json,last_evidence_at_ms,route_id,target_kind,target_value FROM problem_records WHERE route_id=? ORDER BY last_evidence_at_ms DESC LIMIT 30`, value)
		} else {
			problems, e = s.db.QueryContext(ctx, `SELECT problem_kind,severity,confidence,latest_sample_count,latest_evidence_json,last_evidence_at_ms,route_id,target_kind,target_value FROM problem_records WHERE target_kind=? AND target_value=? ORDER BY last_evidence_at_ms DESC LIMIT 30`, kind, value)
		}
		if e != nil {
			return nil, e
		}
		items := make([]map[string]any, 0)
		for problems.Next() {
			var pk, evidence, targetKind, targetValue string
			var severity, confidence, count int
			var last, route int64
			if e = problems.Scan(&pk, &severity, &confidence, &count, &evidence, &last, &route, &targetKind, &targetValue); e != nil {
				problems.Close()
				return nil, e
			}
			var ev any
			_ = json.Unmarshal([]byte(evidence), &ev)
			items = append(items, map[string]any{"kind": pk, "severity": severity, "confidence": confidence, "sample_count": count, "evidence": ev, "last_evidence_at_ms": last, "route_id": route, "target_kind": targetKind, "target": targetValue})
		}
		e = problems.Err()
		problems.Close()
		if e != nil {
			return nil, e
		}
		out["related_problems"] = items
	}
	gapRows, err := s.db.QueryContext(ctx, `SELECT stream,reason,started_at_ms,ended_at_ms FROM collection_gaps WHERE stream IN ('connections','observer','writer') AND started_at_ms>=? ORDER BY started_at_ms DESC LIMIT 100`, time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	gaps := make([]map[string]any, 0)
	for gapRows.Next() {
		var stream, reason string
		var started int64
		var ended sql.NullInt64
		if err = gapRows.Scan(&stream, &reason, &started, &ended); err != nil {
			gapRows.Close()
			return nil, err
		}
		gap := map[string]any{"stream": stream, "reason": reason, "started_at_ms": started}
		if ended.Valid {
			gap["ended_at_ms"] = ended.Int64
		}
		gaps = append(gaps, gap)
	}
	err = gapRows.Err()
	gapRows.Close()
	if err != nil {
		return nil, err
	}
	out["gaps"] = gaps
	out["gaps_30d"] = len(gaps)
	return out, nil
}

func (s *Store) DailyHistory(ctx context.Context, kind, value string, before int64) ([]map[string]any, error) {
	if kind != "domain" && kind != "ip_only" && kind != "proxy_path" && kind != "destination_ip" && kind != "asn" {
		return nil, fmt.Errorf("unsupported detail kind")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_start_ms,route_id,ip_version,network,observed_connections,observed_upload_bytes,observed_download_bytes FROM stats_daily WHERE subject_kind=? AND subject_value=? AND bucket_start_ms IN (SELECT bucket_start_ms FROM stats_daily WHERE subject_kind=? AND subject_value=? AND bucket_start_ms<? GROUP BY bucket_start_ms ORDER BY bucket_start_ms DESC LIMIT 90) ORDER BY bucket_start_ms DESC`, kind, value, kind, value, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := make([]map[string]any, 0)
	for rows.Next() {
		var bucket, route, connections, up, down int64
		var family int
		var network string
		if err = rows.Scan(&bucket, &route, &family, &network, &connections, &up, &down); err != nil {
			return nil, err
		}
		days = append(days, map[string]any{"bucket_start_ms": bucket, "route_id": route, "ip_version": family, "network": network, "observed_connections": connections, "observed_upload_bytes": up, "observed_download_bytes": down})
	}
	return days, rows.Err()
}
