package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"mihomo-observer/internal/storage"
)

type flowRow struct {
	kind, target, ip, rule, payload, chains string
	route, connections, up, down, last      int64
}

func (s *Store) Flows(ctx context.Context, o storage.FlowOptions) (map[string]any, error) {
	if o.End <= o.Start || o.Start < 0 || o.End-o.Start > 366*86400000 {
		return nil, fmt.Errorf("invalid flow range")
	}
	var asOf sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT last_connections_at_ms FROM collector_checkpoint WHERE id=1").Scan(&asOf); err != nil {
		return nil, err
	}
	out := map[string]any{"as_of_ms": asOf.Int64, "live": o.Live, "items": []map[string]any{}}
	var query string
	var args []any
	if o.Live {
		query = `SELECT c.target_kind,COALESCE(c.target_value,''),COALESCE(c.destination_ip,''),c.route_id,COUNT(*),SUM(MAX(0,c.upload_bytes-c.first_seen_upload_bytes)),SUM(MAX(0,c.download_bytes-c.first_seen_download_bytes)),MAX(c.last_seen_at_ms),COALESCE(r.rule,''),COALESCE(r.rule_payload,''),r.chains_json
		FROM connections_raw c JOIN route_paths r ON r.id=c.route_id WHERE c.observation_state='active' AND c.last_seen_at_ms>=? AND (?='' OR c.target_value=?) AND c.target_kind IN ('domain','ip_only') GROUP BY c.target_kind,c.target_value,c.destination_ip,c.route_id ORDER BY MAX(c.last_seen_at_ms) DESC`
		args = []any{asOf.Int64 - 60000, o.Target, o.Target}
	} else {
		var earliest sql.NullInt64
		if err := s.db.QueryRowContext(ctx, "SELECT MIN(bucket_start_ms) FROM stats_hourly WHERE route_id<>0 AND subject_kind IN ('domain','ip_only')").Scan(&earliest); err != nil {
			return nil, err
		}
		table := "stats_hourly"
		start := o.Start
		if !earliest.Valid || o.Start < earliest.Int64 {
			table = "stats_daily"
			start = s.day(o.Start)
			out["granularity"] = "daily"
			out["range_rounded_to_day"] = true
		} else {
			out["granularity"] = "hourly"
		}
		query = `SELECT h.subject_kind,h.subject_value,'' AS ip,h.route_id,SUM(h.observed_connections),SUM(h.observed_upload_bytes),SUM(h.observed_download_bytes),MAX(h.last_seen_at_ms),COALESCE(r.rule,''),COALESCE(r.rule_payload,''),r.chains_json
		FROM ` + table + ` h JOIN route_paths r ON r.id=h.route_id WHERE h.bucket_start_ms>=? AND h.bucket_start_ms<? AND h.route_id<>0 AND h.subject_kind IN ('domain','ip_only') AND (?='' OR h.subject_value=?) GROUP BY h.subject_kind,h.subject_value,h.route_id ORDER BY SUM(h.observed_upload_bytes+h.observed_download_bytes) DESC`
		args = []any{start, o.End, o.Target, o.Target}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var result []flowRow
	for rows.Next() {
		var v flowRow
		if err = rows.Scan(&v.kind, &v.target, &v.ip, &v.route, &v.connections, &v.up, &v.down, &v.last, &v.rule, &v.payload, &v.chains); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(result))
	for _, v := range result {
		if !o.Live && v.kind == "ip_only" {
			v.ip = v.target
		}
		if !o.Live && v.kind != "ip_only" {
			var count int64
			var minIP, maxIP sql.NullString
			var rawUp, rawDown sql.NullInt64
			err = s.db.QueryRowContext(ctx, `SELECT COUNT(*),MIN(destination_ip),MAX(destination_ip),SUM(observed_upload_bytes),SUM(observed_download_bytes) FROM connection_hours WHERE bucket_start_ms>=? AND bucket_start_ms<? AND target_kind=? AND target_value=? AND route_id=?`, o.Start, o.End, v.kind, v.target, v.route).Scan(&count, &minIP, &maxIP, &rawUp, &rawDown)
			if err != nil {
				return nil, err
			}
			if count > 0 && minIP.Valid && minIP.String != "" && minIP.String == maxIP.String && rawUp.Int64 == v.up && rawDown.Int64 == v.down {
				v.ip = minIP.String
			}
		}
		var chains []string
		_ = json.Unmarshal([]byte(v.chains), &chains)
		items = append(items, map[string]any{"target_kind": v.kind, "target": v.target, "destination_ip": v.ip, "route_id": v.route, "connections": v.connections, "upload_bytes": v.up, "download_bytes": v.down, "last_seen_at_ms": v.last, "rule": v.rule, "rule_payload": v.payload, "chains": chains})
	}
	out["items"] = items
	out["generated_at_ms"] = time.Now().UnixMilli()
	return out, nil
}

func (s *Store) FlowSamples(ctx context.Context, target string, route, start, end int64, o storage.PageOptions) (storage.Page, error) {
	page := storage.Page{Items: []map[string]any{}, Limit: o.Limit, Offset: o.Offset}
	if route < 1 || start < 0 || end <= start || validatePage(o) != nil {
		return page, fmt.Errorf("invalid sample range")
	}
	order, err := pageOrder(o.Sort, o.Desc, map[string]string{"time": "last_seen_at_ms", "bytes": "MAX(0,upload_bytes-first_seen_upload_bytes)+MAX(0,download_bytes-first_seen_download_bytes)", "state": "observation_state", "ip": "destination_ip COLLATE NOCASE"}, "time")
	if err != nil {
		return page, err
	}
	where := `FROM connections_raw WHERE route_id=? AND target_value=? AND last_seen_at_ms>=? AND first_seen_at_ms<?`
	args := []any{route, target, start, end}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT target_kind,COALESCE(target_value,''),COALESCE(destination_ip,''),observation_state,first_seen_at_ms,last_seen_at_ms,MAX(0,upload_bytes-first_seen_upload_bytes),MAX(0,download_bytes-first_seen_download_bytes) `+where+` ORDER BY `+order+`,id DESC LIMIT ? OFFSET ?`, append(args, o.Limit, o.Offset)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, value, ip, state string
		var first, last, up, down int64
		if err := rows.Scan(&kind, &value, &ip, &state, &first, &last, &up, &down); err != nil {
			return page, err
		}
		page.Items = append(page.Items, map[string]any{"target_kind": kind, "target": value, "destination_ip": ip, "state": state, "first_seen_at_ms": first, "last_seen_at_ms": last, "upload_bytes": up, "download_bytes": down})
	}
	return page, rows.Err()
}
