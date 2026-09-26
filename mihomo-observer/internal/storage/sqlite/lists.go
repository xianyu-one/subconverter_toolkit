package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"mihomo-observer/internal/storage"
)

func pageOrder(sort string, desc bool, allowed map[string]string, fallback string) (string, error) {
	if sort == "" {
		sort = fallback
	}
	column, ok := allowed[sort]
	if !ok {
		return "", fmt.Errorf("unsupported sort %q", sort)
	}
	direction := " ASC"
	if desc {
		direction = " DESC"
	}
	return column + direction, nil
}

func validatePage(o storage.PageOptions) error {
	if o.Limit < 1 || o.Limit > 200 || o.Offset < 0 || o.Offset > 1000000 {
		return fmt.Errorf("invalid page bounds")
	}
	return nil
}

func (s *Store) TargetsPage(ctx context.Context, o storage.PageOptions) (storage.Page, error) {
	page := storage.Page{Items: []map[string]any{}, Limit: o.Limit, Offset: o.Offset}
	if err := validatePage(o); err != nil {
		return page, err
	}
	order, err := pageOrder(o.Sort, o.Desc, map[string]string{
		"last_seen": "last_seen", "name": "value COLLATE NOCASE", "kind": "kind", "connections": "connections", "bytes": "total_bytes",
	}, "last_seen")
	if err != nil {
		return page, err
	}
	const scope = "FROM stats_daily WHERE route_id=0 AND subject_kind IN ('domain','ip_only','destination_ip')"
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 "+scope+" GROUP BY subject_kind,subject_value)").Scan(&page.Total); err != nil {
		return page, err
	}
	query := `WITH items AS (SELECT subject_kind AS kind,subject_value AS value,SUM(observed_connections) AS connections,SUM(observed_upload_bytes) AS up,SUM(observed_download_bytes) AS down,SUM(observed_upload_bytes+observed_download_bytes) AS total_bytes,MAX(last_seen_at_ms) AS last_seen ` + scope + ` GROUP BY subject_kind,subject_value)
	SELECT kind,value,connections,up,down,last_seen FROM items ORDER BY ` + order + `, kind ASC,value COLLATE NOCASE ASC LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, o.Limit, o.Offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, value string
		var count, up, down, last int64
		if err := rows.Scan(&kind, &value, &count, &up, &down, &last); err != nil {
			return page, err
		}
		page.Items = append(page.Items, map[string]any{"kind": kind, "value": value, "observed_connections": count, "observed_upload_bytes": up, "observed_download_bytes": down, "last_seen_at_ms": last})
	}
	return page, rows.Err()
}

func (s *Store) ProblemsPage(ctx context.Context, now int64, o storage.PageOptions) (storage.Page, error) {
	page := storage.Page{Items: []map[string]any{}, Limit: o.Limit, Offset: o.Offset}
	if err := validatePage(o); err != nil {
		return page, err
	}
	order, err := pageOrder(o.Sort, o.Desc, map[string]string{
		"last_seen": "p.last_evidence_at_ms", "target": "p.target_value COLLATE NOCASE", "kind": "p.problem_kind", "severity": "p.severity", "confidence": "p.confidence", "samples": "p.latest_sample_count",
	}, "severity")
	if err != nil {
		return page, err
	}
	if o.Sort == "severity" && o.Desc {
		order = "p.severity DESC,p.confidence DESC"
	}
	cutoff := now - 7*86400000
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM problem_records WHERE last_evidence_at_ms>=?", cutoff).Scan(&page.Total); err != nil {
		return page, err
	}
	query := `SELECT p.problem_kind,p.target_kind,p.target_value,p.severity,p.confidence,p.latest_sample_count,p.latest_evidence_json,p.last_evidence_at_ms,r.rule,r.rule_payload,r.chains_json FROM problem_records p JOIN route_paths r ON r.id=p.route_id WHERE p.last_evidence_at_ms>=? ORDER BY ` + order + `,p.last_evidence_at_ms DESC,p.id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, cutoff, o.Limit, o.Offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, tk, tv, evidence, chains string
		var sev, conf, n int
		var last int64
		var rule, payload sql.NullString
		if err := rows.Scan(&kind, &tk, &tv, &sev, &conf, &n, &evidence, &last, &rule, &payload, &chains); err != nil {
			return page, err
		}
		var ev any
		_ = json.Unmarshal([]byte(evidence), &ev)
		nextCheck := "Review this target's observed rule, proxy chain and destination details before changing the subscription"
		if kind == "ipv6_observation_difference" {
			nextCheck = "Inspect IPv6 reachability, DNS AAAA and the observed rule; verify any fallback outside Observer"
		}
		page.Items = append(page.Items, map[string]any{"kind": kind, "target_kind": tk, "target": tv, "severity": sev, "confidence": conf, "sample_count": n, "evidence": ev, "next_check": nextCheck, "last_evidence_at_ms": last, "rule": rule.String, "rule_payload": payload.String, "chains_json": chains})
	}
	return page, rows.Err()
}
