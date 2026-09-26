package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
)

// ProblemHistory reads the evidence saved for each completed analysis window.
// Raw connection cleanup does not remove these occurrences.
func (s *Store) ProblemHistory(ctx context.Context, kind, value string, start, end int64) ([]map[string]any, error) {
	if kind != "domain" && kind != "ip_only" && kind != "proxy_path" {
		return nil, fmt.Errorf("unsupported problem history kind")
	}
	filter := "p.target_kind=? AND p.target_value=?"
	args := []any{kind, value, start, end}
	if kind == "proxy_path" {
		filter = "p.route_id=?"
		args = []any{value, start, end}
	}
	query := `SELECT o.id,o.window_start_ms,o.window_end_ms,o.sample_count,o.evidence_json,p.problem_kind,p.route_id,p.target_kind,p.target_value FROM problem_occurrences o JOIN problem_records p ON p.id=o.problem_id WHERE ` + filter + ` AND o.window_end_ms>? AND o.window_start_ms<? ORDER BY o.window_start_ms DESC,o.id DESC LIMIT 1000`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, startAt, endAt, route int64
		var count int
		var evidence, problemKind, targetKind, target string
		if err = rows.Scan(&id, &startAt, &endAt, &count, &evidence, &problemKind, &route, &targetKind, &target); err != nil {
			return nil, err
		}
		var ev any
		_ = json.Unmarshal([]byte(evidence), &ev)
		out = append(out, map[string]any{"id": id, "window_start_ms": startAt, "window_end_ms": endAt, "sample_count": count, "evidence": ev, "kind": problemKind, "route_id": route, "target_kind": targetKind, "target": target})
	}
	return out, rows.Err()
}
